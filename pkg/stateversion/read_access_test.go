package stateversion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSharedGuardUsesReadOnlyExistingLock(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can bypass file permissions")
	}
	for _, version := range []string{"1\n", "2\n", "3\n"} {
		t.Run(version[:1], func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "state")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, versionFile), []byte(version), 0o600); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(parent, ".state"+lockSuffix)
			if err := os.WriteFile(lockPath, nil, 0o400); err != nil {
				t.Fatal(err)
			}
			acquire := AcquireCurrent
			if version == "1\n" {
				acquire = AcquireLegacy
			}
			guard, err := acquire(context.Background(), root)
			if err != nil {
				t.Fatalf("read-only version lock: %v", err)
			}
			defer guard.Close()
			// The same inode must still exclude a migration writer.
			if err := os.Chmod(lockPath, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			if other, err := acquireVersion(ctx, root, syscall.LOCK_EX, int(version[0]-'0')); !errors.Is(
				err,
				context.DeadlineExceeded,
			) {
				if other != nil {
					other.Close()
				}
				t.Fatalf("exclusive guard bypassed reader: %v", err)
			}
		})
	}
}

func TestSharedGuardRejectsFIFOWithoutBlocking(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := syscall.Mkfifo(filepath.Join(parent, ".state"+lockSuffix), 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		guard, err := AcquireLegacy(context.Background(), root)
		if guard != nil {
			guard.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("shared guard accepted a FIFO lock")
		}
	case <-time.After(time.Second):
		t.Fatal("shared guard blocked while opening a FIFO lock")
	}
}
