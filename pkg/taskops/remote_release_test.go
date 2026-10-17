package taskops

import (
	"context"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteAbandonedAcquisitionNeverUsesPrimaryPool(t *testing.T) {
	service, intent, _, _ := remoteInitialLeaseFixture(t)
	ctx := context.Background()
	if _, err := service.store.RequestLaunchLease(ctx, "task-1", intent.Generation, "remote-holder"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		return task.AbandonInitialLaunch(intent.Generation)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReleaseWorktree(ctx, "task-1", intent.Generation); err == nil ||
		!strings.Contains(err.Error(), "native remote worktree transport") {
		t.Fatalf("remote abandoned acquisition reached primary pool: %v", err)
	}
}

func remoteRetiredLeaseFixture(t *testing.T) (*Service, taskstate.Task) {
	t.Helper()
	service, intent, config, _ := remoteInitialLeaseFixture(t)
	ctx := context.Background()
	prepared, err := service.prepareRemoteInitialLease(ctx, "task-1", intent, config)
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.store.Read(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := current.Snapshot()
	snapshot.LaunchIntent = nil
	snapshot.State = taskstate.Done
	snapshot.Definition.Closed = "2026-10-17"
	snapshot.Receipt = &taskstate.Receipt{
		Generation: intent.Generation,
		Mode:       taskstate.Close,
		Lease:      prepared.Lease,
	}
	retired, err := taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return service, retired
}

func TestRemoteLeaseReturnNeverUsesPrimaryPool(t *testing.T) {
	service, retired := remoteRetiredLeaseFixture(t)
	ctx := context.Background()
	snapshot := retired.Snapshot()
	generation := snapshot.Receipt.Generation
	if _, err := service.returnRetiredLease(ctx, retired, generation); err == nil ||
		!strings.Contains(err.Error(), "native remote worktree transport") {
		t.Fatalf("remote lease reached primary pool: %v", err)
	}
	returned := snapshot
	returned.Receipt = &taskstate.Receipt{
		Generation: generation,
		Mode:       taskstate.Close,
		Lease:      snapshot.Receipt.Lease,
	}
	returned.Receipt.Lease.State = taskstate.LeaseReturned
	confirmed, err := taskstate.NewMigrated(returned)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.returnRetiredLease(ctx, confirmed, generation); err != nil {
		t.Fatalf("confirmed return was not idempotent: %v", err)
	}
}
