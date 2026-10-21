package remoteworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/sshcommand"
)

type tunnelProcesses struct {
	starts     atomic.Int32
	status     primary.ProcessStatus
	err        error
	startError error
	stops      atomic.Int32
	stopError  error
}

func TestTunnelManagerReplacesOwnedTunnelAfterAgentDestinationChanges(t *testing.T) {
	for _, ownership := range []struct {
		name            string
		status          primary.ProcessStatus
		failure         error
		wantReplacement bool
	}{
		{"running", primary.ProcessRunning, nil, true},
		{"exited", primary.ProcessExited, nil, true},
		{"unknown", primary.ProcessRunning, primary.ErrProcessOwnership, false},
	} {
		t.Run(ownership.name, func(t *testing.T) {
			root, spec := tunnelFixture(t)
			spec.Authentication = sshcommand.ForwardedAgent
			t.Setenv("SSH_AUTH_SOCK", "/tmp/old-agent.sock")
			processes := &tunnelProcesses{status: primary.ProcessRunning}
			remote := &tunnelRemote{}
			manager, _ := NewTunnelManager(root, processes, remote)
			if err := manager.Ensure(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SSH_AUTH_SOCK", "/tmp/new-agent.sock")
			processes.status, processes.err = ownership.status, ownership.failure
			restarted, _ := NewTunnelManager(root, processes, remote)
			err := restarted.Probe(context.Background(), spec.Owner, spec.MachineID)
			if !ownership.wantReplacement {
				if !errors.Is(err, ErrTunnelOwnership) || processes.starts.Load() != 1 ||
					processes.stops.Load() != 0 {
					t.Fatalf("uncertain tunnel replaced: %v", err)
				}
				return
			}
			if err != nil || processes.starts.Load() != 2 {
				t.Fatalf(
					"old agent destination retained: starts=%d err=%v",
					processes.starts.Load(),
					err,
				)
			}
			store, _ := NewTunnelStore(root)
			if err := store.WithTask(context.Background(), spec.Owner.Task, func(lease *TunnelLease) error {
				record, err := lease.Read()
				if err != nil {
					return err
				}
				if record.Spec.PrimaryAgentSocket != "/tmp/new-agent.sock" || record.Spec.Owner != spec.Owner {
					t.Fatalf("changed worker identity or lost agent destination: %+v", record.Spec)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
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
	prepare      func(context.Context) error
	probe        func(context.Context) error
}

func (remote *tunnelRemote) Prepare(ctx context.Context, _ TunnelSpec) error {
	remote.preparations.Add(1)
	if remote.prepare != nil {
		return remote.prepare(ctx)
	}
	return nil
}

func (remote *tunnelRemote) Probe(ctx context.Context, _ TunnelSpec) error {
	remote.probes.Add(1)
	if remote.probe != nil {
		return remote.probe(ctx)
	}
	return remote.failure
}

func TestTunnelManagerRemoteFailureRetryPolicy(t *testing.T) {
	for _, operation := range []string{"prepare", "probe"} {
		for _, outcome := range []struct {
			name    string
			failure error
		}{
			{"canceled", context.Canceled},
			{"deadline", context.DeadlineExceeded},
			{"disconnected", errors.New("disconnected")},
		} {
			t.Run(operation+"/"+outcome.name, func(t *testing.T) {
				root, spec := tunnelFixture(t)
				processes := &tunnelProcesses{status: primary.ProcessRunning}
				remote := &tunnelRemote{}
				manager, _ := NewTunnelManager(root, processes, remote)
				now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
				manager.Now = func() time.Time { return now }
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fail := func(context.Context) error {
					if outcome.failure == context.Canceled {
						cancel()
					}
					return outcome.failure
				}
				if operation == "prepare" {
					remote.prepare = fail
				} else {
					remote.probe = fail
				}
				err := manager.Ensure(ctx, spec)
				want := ErrTunnelUnreachable
				if outcome.failure == context.Canceled {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Errorf("remote failure returned %v, want %v", err, want)
				}
				if err := manager.store.WithTask(t.Context(), spec.Owner.Task, func(lease *TunnelLease) error {
					record, err := lease.Read()
					if err != nil {
						return err
					}
					if outcome.failure == context.Canceled {
						if record.Failures != 0 || !record.NextRetry.IsZero() {
							t.Errorf("cancellation persisted retry: %+v", record)
						}
					} else if record.Failures != 1 || !record.NextRetry.Equal(now.Add(time.Second)) {
						t.Errorf("remote failure lost retry: %+v", record)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				remote.prepare, remote.probe = nil, nil
				restarted, _ := NewTunnelManager(root, processes, remote)
				restarted.Now = manager.Now
				err = restarted.Ensure(t.Context(), spec)
				if outcome.failure == context.Canceled {
					if err != nil || remote.probes.Load() == 0 {
						t.Fatalf("immediate restart did not probe: %v", err)
					}
				} else if !errors.Is(err, ErrTunnelUnreachable) {
					t.Fatalf("immediate restart bypassed retry: %v", err)
				}
			})
		}
	}
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

func TestTunnelManagerRepairsLegacySharedWorkerEndpoint(t *testing.T) {
	root, spec := tunnelFixture(t)
	processes := &tunnelProcesses{status: primary.ProcessRunning}
	process, err := processes.Start(t.Context(), spec, "/primary/worker.sock")
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewTunnelStore(root)
	if err := store.WithTask(t.Context(), spec.Owner.Task, func(lease *TunnelLease) error {
		return lease.Save(TunnelRecord{Format: 1, Spec: spec, Phase: TunnelRunning, Process: &process})
	}); err != nil {
		t.Fatal(err)
	}
	manager, _ := NewTunnelManager(root, processes, &tunnelRemote{})
	if err := manager.Ensure(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if processes.starts.Load() != 2 || processes.stops.Load() != 1 {
		t.Fatal("shared tunnel was not replaced exactly once")
	}
	if err := store.WithTask(t.Context(), spec.Owner.Task, func(lease *TunnelLease) error {
		record, err := lease.Read()
		if err != nil {
			return err
		}
		if record.Spec.PrimaryWorkerSocket != PrimaryWorkerSocketPath(root, spec.Owner.Task) || record.Spec.Owner != spec.Owner {
			t.Fatal("lost worker identity or task listener destination")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
