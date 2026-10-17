package workerservice

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roman/concierge/pkg/taskmeta"
)

var ErrSocketOccupied = errors.New("worker socket is active or its ownership is uncertain")

// Listen requires the caller to hold the primary host lease for this root.
// That lease excludes another host between the stale-socket check and binding.
func Listen(root string) (*net.UnixListener, error) {
	if _, err := taskmeta.ParseAbsolutePath(root); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "worker.sock")
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, ErrSocketOccupied
		}
		connection, probeError := net.DialTimeout("unix", path, time.Second)
		if probeError == nil {
			connection.Close()
			return nil, ErrSocketOccupied
		}
		if !errors.Is(probeError, syscall.ECONNREFUSED) {
			return nil, ErrSocketOccupied
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			return nil, ErrSocketOccupied
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
