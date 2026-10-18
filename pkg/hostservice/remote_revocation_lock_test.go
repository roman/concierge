package hostservice

import (
	"context"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteRevocationUsesExistingAuthorityBeforeGenerationChange(t *testing.T) {
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
	runner := &retirementSSH{fixture: fixture, processes: processes}
	fixture.server.preparationSSH = runner
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := fixture.server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		task, err := task.CompleteInitialLaunch("gen-1")
		if err != nil {
			return taskstate.Task{}, err
		}
		return task.RevokeWorkerAccess("gen-1")
	}); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.server.store.WithTask(
		ctx,
		"task-1",
		func(locked *taskstate.LockedTask) error {
			return fixture.server.revokeRemoteWorkerTransportLocked(ctx, locked, "gen-1")
		},
	)
	if err != nil || runner.removals != 1 || processes.stops.Load() != 1 ||
		current.Snapshot().Worker.Generation != "gen-1" ||
		current.Snapshot().Completion != nil {
		t.Fatalf("revocation under current task authority: %v", err)
	}
}
