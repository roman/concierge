package remoteworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/stateversion"
)

func tunnelFixture(t *testing.T) (string, TunnelSpec) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, owner := socketOwnerFixture(t)
	return root, TunnelSpec{Owner: owner, MachineID: "machine-1", Target: "machine",
		Tools: Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}}
}

func TestTunnelStoreSurvivesPrimaryRestart(t *testing.T) {
	root, spec := tunnelFixture(t)
	process := primary.ProcessToken{
		PID:          123,
		StartTicks:   "100",
		BootID:       "boot",
		PIDNamespace: "namespace",
	}
	want := TunnelRecord{Format: 1, Spec: spec, Phase: TunnelRunning, Process: &process}
	store, err := NewTunnelStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithTask(context.Background(), spec.Owner.Task, func(lease *TunnelLease) error {
		return lease.Save(want)
	}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewTunnelStore(root)
	if err := restarted.WithTask(context.Background(), spec.Owner.Task, func(lease *TunnelLease) error {
		got, err := lease.Read()
		if err != nil || got == nil || got.Spec != want.Spec || got.Process == nil || *got.Process != process {
			t.Fatalf("persisted ownership lost: record=%+v error=%v", got, err)
		}
		changed := want
		changed.Spec.Owner.Generation = "new-generation"
		if err := lease.Save(changed); !errors.Is(err, ErrTunnelOwnership) {
			t.Fatalf("live ownership was replaced: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelStoreRejectsOldStateBeforeCreatingResources(t *testing.T) {
	root, spec := tunnelFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTunnelStore(root)
	called := false
	err := store.WithTask(context.Background(), spec.Owner.Task, func(*TunnelLease) error {
		called = true
		return nil
	})
	if !errors.Is(err, stateversion.ErrIncompatible) || called {
		t.Fatalf("old root entered tunnel mutation: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "remote-tunnels")); !os.IsNotExist(err) {
		t.Fatalf("old root acquired tunnel resources: %v", err)
	}
}

func TestTunnelStoreRefusesSymlinkDirectory(t *testing.T) {
	root, spec := tunnelFixture(t)
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(root, "remote-tunnels")); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTunnelStore(root)
	if err := store.WithTask(context.Background(), spec.Owner.Task, func(*TunnelLease) error {
		t.Fatal("entered an unowned directory")
		return nil
	}); err == nil {
		t.Fatal("symlink was accepted")
	}
	entries, err := os.ReadDir(other)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink destination changed: %v", err)
	}
}
