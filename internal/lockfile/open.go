// Package lockfile opens persistent advisory-lock files within pinned roots.
package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// OpenShared avoids write access when a persistent lock already exists.
func OpenShared(root *os.Root, name string) (*os.File, error) {
	// A replaced lock can be a FIFO. Let callers reject it without blocking.
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Open(root, name)
	}
	return file, err
}

func Open(root *os.Root, name string) (*os.File, error) {
	// Concurrent O_CREATE without O_EXCL can return ENOENT on Darwin.
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrExist) {
		return root.OpenFile(name, os.O_RDWR, 0)
	}
	return file, err
}
