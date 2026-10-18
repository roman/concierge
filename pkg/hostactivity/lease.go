// Package hostactivity excludes host replacement from launches and landings.
package hostactivity

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("a host operation, launch, landing, or restart is in progress")

// Begin holds a shared lease for one operation that must survive host replacement.
func Begin(
	root string,
) (*os.File, error) {
	return acquire(root, ".host-activity.lock", unix.LOCK_SH)
}

// Quiesce excludes new operations and refuses while an operation holds a lease.
func Quiesce(root string) (*os.File, error) {
	return acquire(root, ".host-activity.lock", unix.LOCK_EX)
}

// Restart serializes callers through shutdown and replacement readiness.
func Restart(
	root string,
) (*os.File, error) {
	return acquire(root, ".host-restart.lock", unix.LOCK_EX)
}

// Departed succeeds only after the old host has released its listener lease.
func Departed(root string) (*os.File, error) { return acquire(root, ".host.lock", unix.LOCK_EX) }

func acquire(root, name string, mode int) (*os.File, error) {
	directory, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	fd, err := unix.Openat(
		int(directory.Fd()),
		name,
		unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0600,
	)
	// Darwin can return ENOENT when concurrent callers use nonexclusive creation.
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(
			int(directory.Fd()),
			name,
			unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
	}
	if err != nil {
		return nil, err
	}
	lease := os.NewFile(uintptr(fd), name)
	if err := unix.Flock(fd, mode|unix.LOCK_NB); err != nil {
		lease.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return lease, nil
}
