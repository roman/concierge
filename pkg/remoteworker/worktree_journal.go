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
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/internal/lockfile"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type worktreeJournal struct {
	root    *os.Root
	name    string
	request WorktreeRequest
}

func withWorktreeJournal(
	ctx context.Context,
	request WorktreeRequest,
	action func(*worktreeJournal) error,
) error {
	if request.Validate() != nil {
		return ErrWorktreeOwnership
	}
	base := request.Location.Base
	if resolved, err := filepath.EvalSymlinks(base); err != nil || resolved != base {
		return ErrWorktreeOwnership
	}
	root, err := workeraccess.OpenPrivateDirectory(base)
	if err != nil {
		return ErrWorktreeOwnership
	}
	defer root.Close()
	if !projectMachineOwner(root, request.Location.Machine) {
		return ErrWorktreeOwnership
	}
	if err := root.Mkdir("leases", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	before, err := root.Lstat("leases")
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return ErrWorktreeOwnership
	}
	directory, err := root.OpenRoot("leases")
	if err != nil {
		return ErrWorktreeOwnership
	}
	defer directory.Close()
	if after, err := directory.Stat("."); err != nil || !os.SameFile(before, after) {
		return ErrWorktreeOwnership
	}
	if err := syncSocketDirectory(root); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(request.Location.PrimaryRoot + "\x00" + request.Task))
	name := hex.EncodeToString(digest[:])
	lock, err := lockfile.Open(directory, name+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	selected, err := directory.Lstat(name + ".lock")
	if err != nil || !privateProjectFile(selected) {
		return ErrWorktreeOwnership
	}
	if opened, err := lock.Stat(); err != nil || !os.SameFile(selected, opened) {
		return ErrWorktreeOwnership
	}
	if err := acquireTunnelLock(ctx, lock); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return action(&worktreeJournal{root: directory, name: name + ".json", request: request})
}

func (journal *worktreeJournal) read() (*worktreeRecord, error) {
	before, err := journal.root.Lstat(journal.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !privateProjectFile(before) {
		return nil, ErrWorktreeOwnership
	}
	file, err := journal.root.OpenFile(journal.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrWorktreeOwnership
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || !os.SameFile(before, opened) {
		return nil, ErrWorktreeOwnership
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, ErrWorktreeOwnership
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record worktreeRecord
	if decoder.Decode(&record) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		record.validate() != nil ||
		record.Request != journal.request {
		return nil, ErrWorktreeOwnership
	}
	if after, err := journal.root.Lstat(journal.name); err != nil || !os.SameFile(before, after) {
		return nil, ErrWorktreeOwnership
	}
	return &record, nil
}

func (journal *worktreeJournal) save(record worktreeRecord) error {
	if record.validate() != nil || record.Request != journal.request {
		return ErrWorktreeOwnership
	}
	previous, err := journal.read()
	if err != nil {
		return err
	}
	if previous == nil {
		if record.Phase != worktreeReserved {
			return ErrWorktreeOwnership
		}
	} else {
		if record.Backend != previous.Backend || !nextWorktreePhase(previous.Phase, record.Phase) {
			return ErrWorktreeOwnership
		}
		if previous.Lease != (taskstate.WorktreeLease{}) {
			expected := previous.Lease
			expected.State = record.Lease.State
			if expected != record.Lease {
				return ErrWorktreeOwnership
			}
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	name := ".lease-" + rand.Text()
	file, err := journal.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer journal.root.Remove(name)
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
	if err := journal.root.Rename(name, journal.name); err != nil {
		return err
	}
	return syncSocketDirectory(journal.root)
}
