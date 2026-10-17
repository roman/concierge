package hostservice

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

type retirementProcesses struct{ hostTunnelProcesses }

func (processes *retirementProcesses) Status(primary.ProcessToken) (primary.ProcessStatus, error) {
	if processes.stops.Load() != 0 {
		return primary.ProcessExited, nil
	}
	return primary.ProcessRunning, nil
}

type retirementSSH struct {
	fixture     *remoteActivationFixture
	processes   *retirementProcesses
	removals    int
	failRemoval error
}

func (runner *retirementSSH) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	if !strings.Contains(arguments[len(arguments)-1], "'worker-secret' 'remove'") {
		return nil, nil, errors.New("unexpected retirement SSH command")
	}
	task, err := runner.fixture.server.store.Read(ctx, "task-1")
	if err != nil {
		return nil, nil, err
	}
	worker := task.Snapshot().Worker
	if worker == nil || worker.Access == nil || !worker.Access.Revoked ||
		runner.processes.stops.Load() != 1 {
		return nil, nil, errors.New("secret removal preceded grant revocation or tunnel stop")
	}
	runner.removals++
	return []byte("status: removed"), nil, runner.failRemoval
}

func TestRemoteTransportRetirementKeepsRevocationAcrossRemovalRetry(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	processes := &retirementProcesses{}
	manager, err := remoteworker.NewTunnelManager(
		fixture.server.config.Root,
		processes,
		fixture.remote,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.tunnels = manager
	if err := fixture.activate(t); err != nil {
		t.Fatal(err)
	}
	fixture.server.preparationMachines = launchLocationCatalog{fixture.profile}
	runner := &retirementSSH{
		fixture:     fixture,
		processes:   processes,
		failRemoval: errors.New("SSH unreachable"),
	}
	fixture.server.preparationSSH = runner
	ctx := context.Background()
	if err := fixture.server.retireRemoteTransport(ctx, "task-1", "gen-1"); err == nil ||
		runner.removals != 0 ||
		processes.stops.Load() != 0 {
		t.Fatal("retired an active generation")
	}
	if _, err := fixture.server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		task, err := task.CompleteInitialLaunch("gen-1")
		if err != nil {
			return taskstate.Task{}, err
		}
		return task.AcceptResult("gen-1", taskstate.Close, "2026-10-17")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Receipt = &taskstate.Receipt{Generation: "older-generation", Mode: taskstate.Retain}
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.retireRemoteTransport(ctx, "task-1", "older-generation"); err != nil ||
		runner.removals != 0 ||
		processes.stops.Load() != 0 {
		t.Fatalf("historical cleanup affected the current worker: %v", err)
	}
	for _, mismatch := range []string{"target", "session", "disabled"} {
		profile := fixture.profile
		switch mismatch {
		case "target":
			profile.Target = "different-target"
		case "session":
			profile.Session = "different-session"
		case "disabled":
			profile.Enabled = false
		}
		fixture.server.preparationMachines = launchLocationCatalog{profile}
		if err := fixture.server.retireRemoteTransport(ctx, "task-1", "gen-1"); err == nil ||
			runner.removals != 0 ||
			processes.stops.Load() != 0 {
			t.Fatalf("changed %s reached cleanup: %v", mismatch, err)
		}
	}
	fixture.server.preparationMachines = launchLocationCatalog{fixture.profile}
	if err := fixture.server.retireRemoteTransport(ctx, "task-1", "gen-1"); !errors.Is(
		err,
		remoteworker.ErrSecretOperationUnconfirmed,
	) {
		t.Fatalf("missing cleanup confirmation: %v", err)
	}
	if runner.removals != 1 || processes.stops.Load() != 1 {
		t.Fatal("cleanup did not reach the owned resources")
	}
	current, err := fixture.server.store.Read(ctx, "task-1")
	if err != nil || current.Snapshot().Worker == nil ||
		!current.Snapshot().Worker.Access.Revoked ||
		current.Snapshot().Completion == nil {
		t.Fatal("uncertain cleanup lost its durable binding")
	}
	runner.failRemoval = nil
	if err := fixture.server.retireRemoteTransport(ctx, "task-1", "gen-1"); err != nil {
		t.Fatal(err)
	}
	if runner.removals != 2 || processes.stops.Load() != 1 || processes.starts.Load() != 1 {
		t.Fatal("cleanup retry replaced or stopped the tunnel again")
	}
}
