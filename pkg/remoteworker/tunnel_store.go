package remoteworker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/roman/concierge/internal/lockfile"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskmeta"
)

type TunnelStore struct{ path string }

func NewTunnelStore(path string) (*TunnelStore, error) {
	if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
		return nil, err
	}
	return &TunnelStore{path: path}, nil
}

// TunnelLease serializes one task's external process ownership. Callers publish
// a starting record before spawning SSH, then publish the captured process token.
type TunnelLease struct {
	root *os.Root
	task string
	name string
}

func (store *TunnelStore) WithTask(
	ctx context.Context,
	task string,
	operation func(*TunnelLease) error,
) error {
	if _, err := taskmeta.ParseTaskID(task); err != nil {
		return err
	}
	guard, err := stateversion.AcquireCurrent(ctx, store.path)
	if err != nil {
		return err
	}
	defer guard.Close()
	root := guard.StateRoot()
	if err := stateversion.RequireMinimum(root, 3); err != nil {
		return err
	}
	if err := root.Mkdir("remote-tunnels", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	before, err := root.Lstat("remote-tunnels")
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return ErrTunnelOwnership
	}
	directory, err := root.OpenRoot("remote-tunnels")
	if err != nil {
		return ErrTunnelOwnership
	}
	defer directory.Close()
	after, err := directory.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return ErrTunnelOwnership
	}
	if err := syncSocketDirectory(root); err != nil {
		return ErrTunnelOwnership
	}
	digest := sha256.Sum256([]byte(task))
	name := hex.EncodeToString(digest[:])
	lock, err := lockfile.Open(directory, name+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	selected, err := directory.Lstat(name + ".lock")
	if err != nil || !privateTunnelFile(selected, 0) {
		return ErrTunnelOwnership
	}
	opened, err := lock.Stat()
	if err != nil || !os.SameFile(selected, opened) {
		return ErrTunnelOwnership
	}
	if err := acquireTunnelLock(ctx, lock); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return operation(&TunnelLease{root: directory, task: task, name: name + ".json"})
}

func privateTunnelFile(info os.FileInfo, kind os.FileMode) bool {
	if info == nil || info.Mode().Type() != kind {
		return false
	}
	permission := os.FileMode(0600)
	if kind == os.ModeDir {
		permission = 0700
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == permission
}

func acquireTunnelLock(ctx context.Context, lock *os.File) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (lease *TunnelLease) Read() (*TunnelRecord, error) {
	before, err := lease.root.Lstat(lease.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !privateTunnelFile(before, 0) {
		return nil, ErrTunnelOwnership
	}
	file, err := lease.root.OpenFile(lease.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrTunnelOwnership
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrTunnelOwnership
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, ErrTunnelOwnership
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record TunnelRecord
	if decoder.Decode(&record) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		record.Validate() != nil || record.Spec.Owner.Task != lease.task {
		return nil, ErrTunnelOwnership
	}
	after, err := lease.root.Lstat(lease.name)
	if err != nil || !os.SameFile(before, after) {
		return nil, ErrTunnelOwnership
	}
	return &record, nil
}

func (lease *TunnelLease) Save(record TunnelRecord) error {
	if record.Validate() != nil || record.Spec.Owner.Task != lease.task {
		return ErrTunnelOwnership
	}
	previous, err := lease.Read()
	if err != nil {
		return err
	}
	if previous != nil && previous.Spec != record.Spec &&
		!tunnelDestinationUpdate(*previous, record) &&
		(previous.Phase != TunnelClosed || previous.Spec.Owner.Generation == record.Spec.Owner.Generation) {
		return ErrTunnelOwnership
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	name := ".tunnel-" + rand.Text()
	file, err := lease.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer lease.root.Remove(name)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := lease.root.Rename(name, lease.name); err != nil {
		return err
	}
	if err := syncSocketDirectory(lease.root); err != nil {
		return ErrTunnelOwnership
	}
	return nil
}

func tunnelDestinationUpdate(previous, next TunnelRecord) bool {
	identity := previous.Spec
	identity.PrimaryAgentSocket = next.Spec.PrimaryAgentSocket
	identity.PrimaryWorkerSocket = next.Spec.PrimaryWorkerSocket
	return identity == next.Spec && previous.Phase == TunnelRetry && next.Phase == TunnelRetry &&
		previous.Process == nil && next.Process == nil
}
