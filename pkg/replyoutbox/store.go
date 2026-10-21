package replyoutbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/roman/concierge/internal/lockfile"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrStorage = errors.New("reply outbox storage is unconfirmed; retain the original reply")
var ErrConflict = errors.New("reply outbox entry conflicts with the original reply")

var ErrRevoked = errors.New(
	"reply access is revoked; stop retries and ask the primary to reconcile the original reply",
)

type Store struct {
	directory     string
	syncDirectory func(*os.Root) error
}

func New(directory string) (*Store, error) {
	if _, err := taskmeta.ParseAbsolutePath(directory); err != nil || directory == "/" {
		return nil, ErrStorage
	}
	return &Store{directory: directory, syncDirectory: syncDirectory}, nil
}

func (store *Store) Path(payload Payload) (string, error) {
	id, err := payload.ID()
	if err != nil {
		return "", err
	}
	return filepath.Join(store.directory, "outbox", id+".json"), nil
}

func (store *Store) Stage(ctx context.Context, payload Payload) (Record, error) {
	return store.update(ctx, payload, func(previous *Record) (Record, error) {
		if previous == nil {
			return Record{Format: 1, Payload: payload}, nil
		}
		if previous.Revoked {
			return *previous, ErrRevoked
		}
		return *previous, nil
	})
}

func (store *Store) Confirm(
	ctx context.Context,
	payload Payload,
	progress Progress,
) (Record, error) {
	return store.update(ctx, payload, func(previous *Record) (Record, error) {
		if previous == nil {
			return Record{}, ErrStorage
		}
		if previous.Revoked {
			return *previous, ErrRevoked
		}
		return mergeProgress(*previous, progress)
	})
}

func (store *Store) Revoke(ctx context.Context, payload Payload) (Record, error) {
	return store.update(ctx, payload, func(previous *Record) (Record, error) {
		if previous == nil {
			return Record{}, ErrStorage
		}
		updated := *previous
		updated.Revoked = true
		return updated, nil
	})
}

func (store *Store) update(
	ctx context.Context,
	payload Payload,
	change func(*Record) (Record, error),
) (Record, error) {
	id, err := payload.ID()
	if err != nil {
		return Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	parent, err := workeraccess.OpenPrivateDirectory(store.directory)
	if err != nil {
		return Record{}, ErrStorage
	}
	defer parent.Close()
	if err := parent.Mkdir("outbox", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return Record{}, ErrStorage
	}
	selected, err := parent.Lstat("outbox")
	if err != nil || !privateInfo(selected, os.ModeDir|0700) {
		return Record{}, ErrStorage
	}
	root, err := parent.OpenRoot("outbox")
	if err != nil {
		return Record{}, ErrStorage
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(selected, opened) || store.syncDirectory(parent) != nil {
		return Record{}, ErrStorage
	}
	lockName := id + ".lock"
	before, err := root.Lstat(lockName)
	if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && !privateInfo(before, 0600) {
		return Record{}, ErrStorage
	}
	lock, err := lockfile.Open(root, lockName)
	if err != nil {
		return Record{}, ErrStorage
	}
	defer lock.Close()
	selected, err = root.Lstat(lockName)
	opened, statError := lock.Stat()
	if err != nil || statError != nil || !privateInfo(selected, 0600) ||
		!os.SameFile(selected, opened) {
		return Record{}, ErrStorage
	}
	if err := acquireLock(ctx, lock); err != nil {
		return Record{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	previous, err := readRecord(root, id+".json")
	if err != nil {
		return Record{}, err
	}
	if previous != nil && !previous.Payload.Equal(payload) {
		return Record{}, ErrConflict
	}
	updated, err := change(previous)
	if err != nil {
		return updated, err
	}
	if previous != nil && reflect.DeepEqual(*previous, updated) {
		return updated, store.syncDirectory(root)
	}
	if err := updated.validate(); err != nil {
		return Record{}, err
	}
	if err := writeRecord(root, id+".json", updated, store.syncDirectory); err != nil {
		return updated, err
	}
	return updated, nil
}

func privateInfo(info os.FileInfo, mode os.FileMode) bool {
	if info == nil || info.Mode().Type() != mode.Type() || info.Mode().Perm() != mode.Perm() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func acquireLock(ctx context.Context, file *os.File) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrStorage
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func readRecord(root *os.Root, name string) (*Record, error) {
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !privateInfo(before, 0600) {
		return nil, ErrStorage
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrStorage
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrStorage
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return nil, ErrStorage
	}
	record, err := DecodeRecord(data)
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) {
		return nil, ErrStorage
	}
	return &record, nil
}

func writeRecord(root *os.Root, name string, record Record, flush func(*os.Root) error) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxRecordBytes {
		return ErrStorage
	}
	temporary := ".outbox-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrStorage
	}
	defer root.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return ErrStorage
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return ErrStorage
	}
	if file.Close() != nil || root.Rename(temporary, name) != nil || flush(root) != nil {
		return ErrStorage
	}
	return nil
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return ErrStorage
	}
	defer file.Close()
	if file.Sync() != nil {
		return ErrStorage
	}
	return nil
}
