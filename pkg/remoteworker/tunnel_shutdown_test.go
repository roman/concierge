package remoteworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/primary"
)

func TestTunnelTeardownPersistsRevocationBeforeUncertainStop(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	manager, _ := NewTunnelManager(root, processes, remote)
	if err := manager.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	processes.stopError = ErrTunnelOwnership
	if err := manager.Revoke(context.Background(), spec.Owner); !errors.Is(
		err,
		ErrTunnelOwnership,
	) {
		t.Fatalf("uncertain stop was accepted: %v", err)
	}
	restarted, _ := NewTunnelManager(root, processes, remote)
	if err := restarted.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelRevoked) {
		t.Fatalf("revoked tunnel restarted: %v", err)
	}
	if processes.starts.Load() != 1 || remote.preparations.Load() != 1 {
		t.Fatal("unconfirmed teardown caused reconnect or socket cleanup")
	}
	processes.stopError = nil
	if err := restarted.Revoke(context.Background(), spec.Owner); err != nil {
		t.Fatal(err)
	}
	stops, preparations := processes.stops.Load(), remote.preparations.Load()
	if stops != 2 || preparations != 2 {
		t.Fatal("teardown did not confirm the process and remote socket")
	}
	if err := restarted.Revoke(context.Background(), spec.Owner); err != nil {
		t.Fatal(err)
	}
	if processes.stops.Load() != stops || remote.preparations.Load() != preparations {
		t.Fatal("confirmed teardown was repeated")
	}
}

func TestTunnelProbeAndTeardownRefuseDifferentOwnership(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	manager, _ := NewTunnelManager(root, processes, remote)
	if err := manager.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	other := spec.Owner
	other.Verifier = strings.Repeat("0", 64)
	if err := manager.Probe(context.Background(), other, spec.MachineID); !errors.Is(
		err,
		ErrTunnelOwnership,
	) {
		t.Fatalf("different verifier authorized tunnel recovery: %v", err)
	}
	if err := manager.Probe(context.Background(), spec.Owner, "other-machine"); !errors.Is(
		err,
		ErrTunnelOwnership,
	) {
		t.Fatalf("different machine authorized tunnel recovery: %v", err)
	}
	if err := manager.Revoke(context.Background(), other); !errors.Is(err, ErrTunnelOwnership) {
		t.Fatalf("different verifier authorized teardown: %v", err)
	}
	if processes.stops.Load() != 0 || processes.starts.Load() != 1 || remote.probes.Load() != 1 {
		t.Fatal("different ownership reached an external operation")
	}
}
