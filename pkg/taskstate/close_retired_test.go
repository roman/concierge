package taskstate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func retainedTaskForClosure(t *testing.T) Task {
	t.Helper()
	task, err := runningTaskForRestart(t).PlaceHold(Hold{Reason: "Review result", Kind: "primary"})
	if err == nil {
		task, err = task.AcceptResult("gen-1", Retain, "")
	}
	if err == nil {
		task, err = task.RetireResult("gen-1")
	}
	if err != nil {
		t.Fatal(err)
	}
	snapshot := task.Snapshot()
	input := launchForTest()
	snapshot.Receipt.Lease = WorktreeLease{ID: "lease", Holder: "holder", Project: input.Project,
		Path: input.Worktree, Branch: input.Branch, BaseCommit: input.BaseCommit, State: LeaseHeld}
	task, err = NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCloseRetiredPreservesHeldResources(t *testing.T) {
	task := retainedTaskForClosure(t)
	snapshot := task.Snapshot()
	prior := *snapshot.Receipt
	prior.Generation, prior.Lease.ID, prior.Lease.Path = "prior-generation", "prior-lease", "/prior-worktree"
	snapshot.PriorReceipts = []Receipt{prior}
	abandonedLease := prior.Lease
	abandonedLease.ID, abandonedLease.Path = "abandoned-lease", "/abandoned-worktree"
	snapshot.AbandonedLaunches = []LaunchAbandonment{{Generation: "abandoned-generation",
		Lease: abandonedLease, LeaseHolder: abandonedLease.Holder, Project: abandonedLease.Project,
		Worktree: abandonedLease.Path, Branch: abandonedLease.Branch, BaseCommit: abandonedLease.BaseCommit}}
	var err error
	task, err = NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	before := task.Snapshot()
	closed, err := task.CloseRetired("gen-1", "Superseded by replacement", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	after := closed.Snapshot()
	if closed.State() != Done || after.Definition.Closed != "2026-10-20" ||
		after.Closure == nil || after.Closure.Reason != "Superseded by replacement" ||
		!reflect.DeepEqual(before.Receipt, after.Receipt) ||
		!reflect.DeepEqual(before.PriorReceipts, after.PriorReceipts) ||
		!reflect.DeepEqual(before.AbandonedLaunches, after.AbandonedLaunches) ||
		!reflect.DeepEqual(before.Definition.Hold, after.Definition.Hold) {
		t.Fatalf("closure lost the reason, date, hold, or retired resources: %+v", after)
	}
	data, err := closed.Encode()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := reloaded.CloseRetired("gen-1", "Superseded by replacement", "2026-10-20")
	if err != nil || !reflect.DeepEqual(retried.Snapshot(), after) {
		t.Fatalf("exact retry changed the closed task: %v", err)
	}
	candidate, err := taskForTransition(t).ReserveInitialLaunch(launchForTest())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(checkWorkerIdentity([]Task{closed}, candidate), ErrWorkerConflict) {
		t.Fatal("closure released the retained lease reservation")
	}
}

func TestCloseRetiredRejectsMissingHoldAndBlankDurableReason(t *testing.T) {
	closed, err := retainedTaskForClosure(t).CloseRetired("gen-1", "Superseded", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing hold", "blank reason"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := closed.Snapshot()
			if mode == "missing hold" {
				snapshot.Definition.Hold = nil
			} else {
				snapshot.Closure.Reason = strings.Repeat(" ", 3)
			}
			if _, err := NewMigrated(snapshot); err == nil {
				t.Fatal("accepted an invalid durable closure")
			}
		})
	}
	if _, err := retainedTaskForClosure(t).CloseRetired("gen-1", "   ", "2026-10-20"); err == nil {
		t.Fatal("accepted a blank reason")
	}
}

func TestCloseRetiredAbandonedLaunchAndRejectActiveIntent(t *testing.T) {
	task, err := taskForTransition(t).ReserveInitialLaunch(launchForTest())
	if err == nil {
		task, err = task.PlaceHold(Hold{Reason: "Parked"})
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.CloseRetired("gen-1", "Cancelled", "2026-10-20"); err == nil {
		t.Fatal("closed an active launch intent")
	}
	task, err = task.AbandonInitialLaunch("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	closed, err := task.CloseRetired("gen-1", "Cancelled", "2026-10-20")
	if err != nil ||
		!reflect.DeepEqual(task.Snapshot().AbandonedLaunches, closed.Snapshot().AbandonedLaunches) {
		t.Fatalf("closure changed the abandoned launch receipt: %v", err)
	}
	store, err := NewStore(versionTwoRoot(t))
	if err == nil {
		err = store.Create(context.Background(), closed)
	}
	if err != nil {
		t.Fatal(err)
	}
	snapshot := closed.Snapshot()
	snapshot.Closure.Generation = "unrecorded"
	if _, err := NewMigrated(snapshot); err == nil {
		t.Fatal("accepted a closure without retired generation evidence")
	}
}

func TestCloseRetiredRejectsUnfinishedOrChangedRequests(t *testing.T) {
	for _, mode := range []string{"wrong generation", "missing reason", "invalid date", "unheld", "worker", "changed retry"} {
		t.Run(mode, func(t *testing.T) {
			task := retainedTaskForClosure(t)
			generation, reason, date := "gen-1", "Superseded", "2026-10-20"
			switch mode {
			case "wrong generation":
				generation = "gen-2"
			case "missing reason":
				reason = ""
			case "invalid date":
				date = "2026-02-30"
			case "unheld":
				task, _ = task.ReleaseHold()
			case "worker":
				task = runningTaskForRestart(t)
			case "changed retry":
				var err error
				task, err = task.CloseRetired(generation, reason, date)
				if err != nil {
					t.Fatal(err)
				}
				reason = "Different reason"
			}
			if _, err := task.CloseRetired(generation, reason, date); err == nil {
				t.Fatal("closed an unfinished task or accepted a changed request")
			}
		})
	}
}
