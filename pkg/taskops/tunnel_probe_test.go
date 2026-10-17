package taskops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskstate"
)

type fakeTunnelProbe struct {
	task       string
	generation string
	calls      int
	failure    error
	onProbe    func()
}

func (probe *fakeTunnelProbe) Probe(ctx context.Context, task, generation string) error {
	if _, bounded := ctx.Deadline(); !bounded {
		return errors.New("unbounded tunnel probe")
	}
	probe.calls++
	if probe.onProbe != nil {
		probe.onProbe()
	}
	if task != probe.task || generation != probe.generation {
		return errors.New("wrong tunnel identity")
	}
	return probe.failure
}

func TestTunnelRecordFailureRemainsFatalDuringHerdrPartition(t *testing.T) {
	_, input, runner, original := remoteObservedFixture(t)
	probe := &fakeTunnelProbe{task: input.TaskID, generation: input.Generation}
	worker := original.WithTunnelProbe(probe)
	initial := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	now := initial
	worker.now = func() time.Time { return now }
	if err := worker.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = initial.Add(time.Second)
	runner.failure = errors.New("Herdr unavailable")
	probe.onProbe = func() { now = initial.Add(-time.Second) }
	err := worker.Observe(context.Background())
	if !errors.Is(err, taskstate.ErrInvalidTransition) || errors.Is(err, supervise.ErrUnreachable) {
		t.Fatalf("record failure was treated as a retryable partition: %v", err)
	}
}

func TestRemoteTunnelBackoffIsIndependentOfHerdr(t *testing.T) {
	service, input, runner, original := remoteObservedFixture(t)
	probe := &fakeTunnelProbe{task: input.TaskID, generation: input.Generation}
	worker := original.WithTunnelProbe(probe)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }
	if err := worker.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	probe.failure = errors.New("tunnel unavailable")
	if err := worker.Observe(context.Background()); !errors.Is(err, supervise.ErrUnreachable) {
		t.Fatalf("tunnel failure: %v", err)
	}
	current, err := service.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	bound := current.Snapshot().Worker
	if bound.Generation != input.Generation ||
		bound.Connections.Herdr.Status != taskstate.ConnectionReachable ||
		bound.Connections.Tunnel.Status != taskstate.ConnectionUnreachable {
		t.Fatalf("tunnel failure changed independent state: %+v", bound)
	}
	herdrCalls, tunnelCalls := runner.calls, probe.calls
	if err := worker.Observe(context.Background()); !errors.Is(err, supervise.ErrUnreachable) {
		t.Fatalf("backoff: %v", err)
	}
	if runner.calls <= herdrCalls || probe.calls != tunnelCalls {
		t.Fatal("tunnel backoff suppressed Herdr or retried the tunnel early")
	}

	now = bound.Connections.Tunnel.NextRetry
	probe.failure = nil
	runner.failure = errors.New("Herdr unavailable")
	if err := worker.Observe(context.Background()); !errors.Is(err, supervise.ErrUnreachable) {
		t.Fatalf("Herdr failure: %v", err)
	}
	current, err = service.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	connections := current.Snapshot().Worker.Connections
	if connections.Herdr.Status != taskstate.ConnectionUnreachable ||
		connections.Tunnel.Status != taskstate.ConnectionReachable {
		t.Fatalf("Herdr failure suppressed tunnel recovery: %+v", connections)
	}
	if original.tunnelProbe != nil {
		t.Fatal("probe configuration mutated the original worker")
	}
}
