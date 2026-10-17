package remoteworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

type tunnelProcesses struct {
	starts     atomic.Int32
	status     primary.ProcessStatus
	err        error
	startError error
	stops      atomic.Int32
	stopError  error
}

func (processes *tunnelProcesses) Start(
	context.Context,
	TunnelSpec,
	string,
) (primary.ProcessToken, error) {
	processes.starts.Add(1)
	if processes.startError != nil {
		return primary.ProcessToken{}, processes.startError
	}
	return primary.ProcessToken{
		PID:          123,
		StartTicks:   "100",
		BootID:       "boot",
		PIDNamespace: "namespace",
	}, nil
}

func TestTunnelManagerRetriesConfirmedStartFailure(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning,
		startError: errors.Join(ErrTunnelNotStarted, ErrTunnelUnreachable)}
	remote := &tunnelRemote{}
	manager, _ := NewTunnelManager(root, processes, remote)
	now := time.Now().UTC()
	manager.Now = func() time.Time { return now }
	if err := manager.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelUnreachable) {
		t.Fatalf("confirmed pre-spawn failure lost retryability: %v", err)
	}
	now = now.Add(time.Second)
	processes.startError = nil
	if err := manager.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("confirmed failed start could not recover: %v", err)
	}
	if processes.starts.Load() != 2 {
		t.Fatal("failed start was not retried")
	}
}

func (processes *tunnelProcesses) Status(primary.ProcessToken) (primary.ProcessStatus, error) {
	return processes.status, processes.err
}

func (processes *tunnelProcesses) Stop(context.Context, primary.ProcessToken) error {
	processes.stops.Add(1)
	if processes.stopError != nil {
		return processes.stopError
	}
	processes.status = primary.ProcessExited
	return nil
}

type tunnelRemote struct {
	preparations atomic.Int32
	probes       atomic.Int32
	failure      error
}

func (remote *tunnelRemote) Prepare(context.Context, TunnelSpec) error {
	remote.preparations.Add(1)
	return nil
}

func (remote *tunnelRemote) Probe(context.Context, TunnelSpec) error {
	remote.probes.Add(1)
	return remote.failure
}

func TestTunnelManagerReusesPersistedProcessAfterPrimaryRestart(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	manager, err := NewTunnelManager(root, processes, remote)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewTunnelManager(root, processes, remote)
	if err := restarted.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if processes.starts.Load() != 1 || remote.preparations.Load() != 1 ||
		remote.probes.Load() != 2 {
		t.Fatalf("restart created another tunnel: starts=%d preparations=%d probes=%d",
			processes.starts.Load(), remote.preparations.Load(), remote.probes.Load())
	}
}

func TestTunnelManagerPersistsBackoffWithoutReplacingUnreachableProcess(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{failure: errors.New("disconnected")}
	manager, _ := NewTunnelManager(root, processes, remote)
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if err := manager.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelUnreachable) {
		t.Fatalf("lost connection was not unreachable: %v", err)
	}
	restarted, _ := NewTunnelManager(root, processes, remote)
	restarted.Now = manager.Now
	if err := restarted.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelUnreachable) {
		t.Fatalf("backoff was lost: %v", err)
	}
	if remote.probes.Load() != 1 || processes.starts.Load() != 1 {
		t.Fatal("restart bypassed backoff or replaced an unreachable process")
	}
	now = now.Add(time.Second)
	remote.failure = nil
	if err := restarted.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if processes.starts.Load() != 1 || remote.preparations.Load() != 1 ||
		remote.probes.Load() != 2 {
		t.Fatal("recovery replaced the existing tunnel")
	}
}

func TestTunnelManagerRetainsUncertainProcessOwnership(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	manager, _ := NewTunnelManager(root, processes, remote)
	if err := manager.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	processes.err = primary.ErrProcessOwnership
	if err := manager.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelOwnership) {
		t.Fatalf("uncertain process was accepted: %v", err)
	}
	if processes.starts.Load() != 1 || remote.preparations.Load() != 1 {
		t.Fatal("uncertain process caused cleanup or a new tunnel")
	}
}

func TestTunnelManagerSerializesConcurrentRecovery(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			manager, err := NewTunnelManager(root, processes, remote)
			if err == nil {
				err = manager.Ensure(context.Background(), spec)
			}
			failures <- err
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if processes.starts.Load() != 1 || remote.preparations.Load() != 1 {
		t.Fatal("concurrent recovery created another tunnel")
	}
}

func TestTunnelManagerDoesNotGuessAfterInterruptedSpawn(t *testing.T) {
	root, spec := tunnelFixture(t)
	store, _ := NewTunnelStore(root)
	if err := store.WithTask(context.Background(), spec.Owner.Task, func(lease *TunnelLease) error {
		return lease.Save(TunnelRecord{Format: 1, Spec: spec, Phase: TunnelStarting})
	}); err != nil {
		t.Fatal(err)
	}
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	remote := &tunnelRemote{}
	manager, _ := NewTunnelManager(root, processes, remote)
	if err := manager.Ensure(context.Background(), spec); !errors.Is(err, ErrTunnelOwnership) {
		t.Fatalf("interrupted spawn was retried: %v", err)
	}
	if processes.starts.Load() != 0 || remote.preparations.Load() != 0 ||
		remote.probes.Load() != 0 {
		t.Fatal("interrupted spawn caused another external operation")
	}
}
