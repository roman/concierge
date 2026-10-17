package remoteworker

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPrepareSocketRetainsChangedSocket(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	probe := func(context.Context, string) (net.Conn, error) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
		return nil, syscall.ECONNREFUSED
	}
	if err := prepareSocket(context.Background(), directory, owner, probe); !errors.Is(
		err,
		ErrSocketOwnership,
	) {
		t.Fatalf("changed socket permitted cleanup: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Fatalf("replacement was changed: %q %v", data, err)
	}
}

func TestPrepareSocketCancellationDoesNotClaimDirectory(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := PrepareSocket(ctx, directory, owner); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "socket-owner.json")); !os.IsNotExist(err) {
		t.Fatalf("cancelled preparation wrote ownership metadata: %v", err)
	}
}

func TestPrepareSocketRemovesOnlyOwnedStaleListener(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	before, _ := os.Lstat(path)
	if err := PrepareSocket(context.Background(), directory, owner); !errors.Is(
		err,
		ErrSocketActive,
	) {
		t.Fatalf("active listener was not retained: %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("preparation changed an active socket")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := PrepareSocket(context.Background(), directory, owner); err != nil {
			t.Fatalf("owned stale socket recovery: %v", err)
		}
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stale socket retained: %v", err)
	}
}

func TestPrepareSocketRetainsPathWhenProbeIsUncertain(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	before, _ := os.Lstat(path)
	probe := func(context.Context, string) (net.Conn, error) { return nil, os.ErrPermission }
	if err := prepareSocket(context.Background(), directory, owner, probe); !errors.Is(
		err,
		ErrSocketOwnership,
	) {
		t.Fatalf("uncertain probe permitted cleanup: %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("uncertain probe changed socket path")
	}
}

func TestPrepareSocketRefusesOtherFileTypes(t *testing.T) {
	for _, kind := range []string{"regular file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory, owner := socketOwnerFixture(t)
			if err := ClaimSocketDirectory(directory, owner); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "worker.sock")
			var err error
			if kind == "symlink" {
				err = os.Symlink("secret", path)
			} else {
				err = os.WriteFile(path, []byte("retain"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(path)
			if err := PrepareSocket(context.Background(), directory, owner); !errors.Is(
				err,
				ErrSocketOwnership,
			) {
				t.Fatalf("foreign path permitted cleanup: %v", err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("foreign file changed")
			}
		})
	}
}
