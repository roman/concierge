package taskstate

import (
	"reflect"
	"testing"
)

func TestRecoverOrphanedPreservesPendingWorkerAndLease(t *testing.T) {
	for _, phase := range []string{"reserved", "advanced", "legacy"} {
		t.Run(phase, func(t *testing.T) {
			snapshot := runningTaskForRestart(t).Snapshot()
			input := launchForTest()
			snapshot.Worker.Lease = WorktreeLease{
				ID:         "lease",
				Holder:     "holder",
				Project:    input.Project,
				Path:       input.Worktree,
				Branch:     input.Branch,
				BaseCommit: input.BaseCommit,
				State:      LeaseHeld,
			}
			snapshot.Worker.PendingLaunch = "gen-2"
			if phase == "advanced" {
				snapshot.Worker.Generation = "gen-2"
				snapshot.Worker.Restarts = []RestartPlan{{From: "gen-1", To: "gen-2"}}
			}
			if phase == "legacy" {
				snapshot.Worker.PendingGeneration = "gen-2"
			}
			task, err := NewMigrated(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			generation := snapshot.Worker.Generation
			closed, err := task.RecoverOrphaned(
				generation,
				"Endpoint absent; work superseded",
				"2026-10-20",
			)
			if err != nil {
				t.Fatal(err)
			}
			after := closed.Snapshot()
			if after.State != Done || after.Worker != nil || after.LaunchIntent != nil ||
				after.Closure == nil || !reflect.DeepEqual(after.Closure.Worker, snapshot.Worker) {
				t.Fatalf("lost pending worker identity: %+v", after)
			}
			lease, found := closed.RetiredLease(generation)
			if !found || lease != snapshot.Worker.Lease {
				t.Fatal("recovery changed the held lease")
			}
			candidate, err := taskForTransition(t).ReserveInitialLaunch(input)
			if err != nil || checkWorkerIdentity([]Task{closed}, candidate) == nil {
				t.Fatal("recovery released the worktree reservation")
			}
			data, err := closed.Encode()
			if err != nil {
				t.Fatal(err)
			}
			reloaded, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			retried, err := reloaded.RecoverOrphaned(
				generation,
				"Endpoint absent; work superseded",
				"2026-10-20",
			)
			if err != nil || !reflect.DeepEqual(retried.Snapshot(), after) {
				t.Fatalf("recovery retry changed the receipt: %v", err)
			}
			if _, err := closed.RecoverOrphaned("other", "Endpoint absent; work superseded", "2026-10-20"); err == nil {
				t.Fatal("recovery accepted a different generation")
			}
			if _, err := closed.AbandonInitialLaunch(generation); err == nil {
				t.Fatal("initial abandonment accepted an orphaned restart receipt")
			}
			if _, err := closed.DiscardWorker(generation); err == nil {
				t.Fatal("discard accepted an orphaned restart receipt")
			}
			// Explicit lease release must remain possible without changing the archive.
			returned, err := closed.RecordLeaseReturned(generation, lease)
			if err != nil ||
				!reflect.DeepEqual(returned.Snapshot().Closure.Worker, snapshot.Worker) {
				t.Fatalf("explicit return changed the worker archive: %v", err)
			}
		})
	}
}

func TestRecoverOrphanedRejectsOtherWorkerStates(t *testing.T) {
	for _, mode := range []string{"finished launch", "no lease", "wrong generation", "blank reason", "invalid date"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := runningTaskForRestart(t).Snapshot()
			input := launchForTest()
			snapshot.Worker.PendingLaunch = "gen-2"
			snapshot.Worker.Lease = WorktreeLease{
				ID:         "lease",
				Holder:     "holder",
				Project:    input.Project,
				Path:       input.Worktree,
				Branch:     input.Branch,
				BaseCommit: input.BaseCommit,
				State:      LeaseHeld,
			}
			generation, reason, date := "gen-1", "Superseded", "2026-10-20"
			switch mode {
			case "finished launch":
				snapshot.Worker.PendingLaunch = ""
			case "no lease":
				snapshot.Worker.Lease = WorktreeLease{}
			case "wrong generation":
				generation = "gen-2"
			case "blank reason":
				reason = "   "
			case "invalid date":
				date = "2026-02-30"
			}
			task, err := NewMigrated(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := task.RecoverOrphaned(generation, reason, date); err == nil {
				t.Fatal("recovered an unrelated worker or invalid decision")
			}
		})
	}
}

func TestRecoverOrphanedPreservesRemotePendingWorker(t *testing.T) {
	task, err := remoteResultTask(t).ReserveLaunch("gen-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.RecoverOrphaned("gen-1", "Superseded", "2026-10-20"); err == nil {
		t.Fatal("recovered a remote worker without transport reconciliation")
	}
}
