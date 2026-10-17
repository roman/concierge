package taskops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/notify"
)

type partitionRunner struct {
	delegate herdr.MachineRunner
	failure  error
	calls    int
}

func (runner *partitionRunner) RunMachine(
	ctx context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	runner.calls++
	if runner.failure != nil {
		return nil, nil, runner.failure
	}
	return runner.delegate.RunMachine(ctx, machine, args...)
}

func remoteObservedFixture(
	t *testing.T,
) (*Service, AdoptionInput, *partitionRunner, *SupervisedWorker) {
	t.Helper()
	service, input, local := adoptionFixture(t)
	service.workerCommands = CommandPaths{Concierge: "/tools/concierge", Tincan: "/tools/tincan"}
	if err := taskstate.UpgradeVersionTwo(context.Background(), service.root, service.root+"-backup"); err != nil {
		t.Fatal(err)
	}
	input.Machine, input.Worktree = "machine-1", "/Users/remote/worktree"
	local.worktree = input.Worktree
	runner := &partitionRunner{delegate: &remoteAdoptionRunner{runner: local}}
	client := herdr.Client{MachineRunner: runner}
	if _, err := service.AdoptWorker(context.Background(), input, client); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), input.TaskID); err != nil {
		t.Fatal(err)
	}
	worker, err := service.SupervisedWorker(input.TaskID, service.root+"-messages", client)
	if err != nil {
		t.Fatal(err)
	}
	return service, input, runner, worker
}

func TestRemoteProbeBackoffSurvivesControllerReload(t *testing.T) {
	service, input, runner, worker := remoteObservedFixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }
	if err := worker.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := service.store.Read(context.Background(), input.TaskID)
	if err != nil || current.Snapshot().Worker.Connections == nil {
		t.Fatalf("healthy remote probe was not persisted: %v", err)
	}
	now = now.Add(time.Second)
	runner.failure = errors.New("SSH unavailable")
	if err := worker.Observe(context.Background()); !errors.Is(err, supervise.ErrUnreachable) {
		t.Fatalf("remote failure: %v", err)
	}
	current, err = service.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	bound := current.Snapshot().Worker
	if bound.Generation != input.Generation || bound.Worktree != input.Worktree ||
		bound.Connections.Herdr.Status != taskstate.ConnectionUnreachable {
		t.Fatalf("partition changed the binding: %+v", bound)
	}
	attempts := runner.calls
	restored, err := service.SupervisedWorker(
		input.TaskID,
		service.root+"-messages",
		herdr.Client{MachineRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	restored.now = worker.now
	if err := restored.Observe(context.Background()); !errors.Is(err, supervise.ErrUnreachable) ||
		runner.calls != attempts {
		t.Fatalf("controller reload ignored backoff: calls=%d error=%v", runner.calls, err)
	}
	if err := restored.Prompt(context.Background(), input.TaskID, notify.DoorbellText); !errors.Is(
		err,
		supervise.ErrUnreachable,
	) {
		t.Fatalf("unreachable doorbell: %v", err)
	}
	if runner.calls != attempts {
		t.Fatal("doorbell bypassed the persistent retry deadline")
	}
	now = bound.Connections.Herdr.NextRetry
	runner.failure = nil
	if err := restored.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err = service.store.Read(context.Background(), input.TaskID)
	if err != nil ||
		current.Snapshot().Worker.Connections.Herdr.Status != taskstate.ConnectionReachable ||
		current.Snapshot().Worker.Generation != input.Generation {
		t.Fatalf("recovery: %+v %v", current.Snapshot(), err)
	}
}
