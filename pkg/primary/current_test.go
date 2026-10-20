package primary

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestOwnerOperationRejectsStaleClaim(t *testing.T) {
	for _, trial := range []struct {
		name      string
		live      bool
		wantError bool
	}{
		{"current", true, false}, {"exited", false, true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			root := t.TempDir()
			owner := testIdentity("codex", "selected", 1)
			if err := writeOwner(root, owner); err != nil {
				t.Fatal(err)
			}
			called := false
			err := withCurrentOwner(
				root,
				func(Identity) (bool, error) { return trial.live, nil },
				func(actual Identity) error {
					called = true
					if actual != owner {
						return errors.New("wrong owner")
					}
					return nil
				},
			)
			if (err != nil) != trial.wantError || called == trial.wantError {
				t.Fatalf("called %v, error %v", called, err)
			}
		})
	}
}

func TestOwnerOperationHoldsReplacementFence(t *testing.T) {
	root := t.TempDir()
	if err := writeOwner(root, testIdentity("codex", "owner", 1)); err != nil {
		t.Fatal(err)
	}
	err := withCurrentOwner(
		root,
		func(Identity) (bool, error) { return true, nil },
		func(Identity) error {
			_, err := withClaimLock(
				root,
				syscall.LOCK_EX|syscall.LOCK_NB,
				func() (Status, error) { return Owner, nil },
			)
			if !errors.Is(err, ErrClaimBusy) {
				t.Fatalf("replacement entered owner operation: %v", err)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withClaimLock(root, syscall.LOCK_EX|syscall.LOCK_NB, func() (Status, error) { return Owner, nil }); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerOperationRejectsForeignNamespace(t *testing.T) {
	token, err := CaptureProcessToken(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	owner := Identity{Version: 2, Kind: "codex", SessionID: "owner", PID: token.PID,
		StartTicks: token.StartTicks, BootID: token.BootID, PIDNamespace: token.PIDNamespace + "-foreign"}
	root := t.TempDir()
	if err := writeOwner(root, owner); err != nil {
		t.Fatal(err)
	}
	called := false
	err = WithOwner(root, func(Identity) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("foreign namespace admitted: called %v, error %v", called, err)
	}
}
