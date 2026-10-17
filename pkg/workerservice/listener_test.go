package workerservice

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerListenerPreservesOccupiedPaths(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "active socket"} {
		t.Run(kind, func(t *testing.T) {
			directory, err := os.MkdirTemp("", "worker-listener-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(directory)
			path := filepath.Join(directory, "worker.sock")
			switch kind {
			case "file":
				err = os.WriteFile(path, []byte("retain"), 0600)
			case "symlink":
				err = os.Symlink("other", path)
			case "active socket":
				var listener *net.UnixListener
				listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if listener != nil {
					defer listener.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(path)
			if listener, err := Listen(directory); err == nil {
				listener.Close()
				t.Fatal("occupied worker socket path was replaced")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("occupied path changed: %v", err)
			}
		})
	}
}

func TestWorkerListenerRecoversAbandonedSocket(t *testing.T) {
	directory, err := os.MkdirTemp("", "worker-listener-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "worker.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("worker socket permissions: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "host.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker listener touched host.sock: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker listener left its socket: %v", err)
	}
}
