package taskstate

import (
	"errors"
	"testing"
)

func runningTask(t *testing.T) Task {
	t.Helper()
	worker := workerForTransition()
	worker.Workspace, worker.Tab = "w1", "t1"
	worker.Lease = WorktreeLease{
		ID:         "lease-1",
		Holder:     "holder",
		Project:    worker.Project,
		Path:       worker.Worktree,
		Branch:     "task/work",
		BaseCommit: "1111111111111111111111111111111111111111",
		State:      LeaseHeld,
	}
	task, err := NewMigrated(
		Snapshot{Definition: taskForTransition(t).Definition(), State: InFlight, Worker: &worker},
	)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestDiscardWorkerPreservesUnfinishedTaskWithoutResult(t *testing.T) {
	task := runningTask(t)
	discarded, err := task.DiscardWorker("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := discarded.Snapshot()
	if snapshot.State != Queued || snapshot.Worker != nil || snapshot.Completion != nil ||
		snapshot.Receipt != nil {
		t.Fatalf("discard manufactured a result: %+v", snapshot)
	}
	receipt, found := discarded.LaunchAbandonment("gen-1")
	if !found || receipt.Kind != DiscardedWorker || !receipt.WorkerRetired {
		t.Fatalf("missing discard evidence: %+v", receipt)
	}
	data, err := discarded.Encode()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.DiscardWorker("gen-1"); err != nil {
		t.Fatal(err)
	}
	if err := loaded.CheckAbandonInitialLaunch("gen-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("discard treated as unfinished launch: %v", err)
	}
}

func TestDiscardWorkerRejectsOtherGenerationsAndResults(t *testing.T) {
	for _, test := range []struct {
		name       string
		generation string
		completion bool
	}{
		{"missing generation", "", false},
		{"different generation", "gen-2", false},
		{"accepted result", "gen-1", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := runningTask(t)
			if test.completion {
				var err error
				task, err = task.AcceptResult("gen-1", Close, "2026-10-20")
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := task.DiscardWorker(test.generation); !errors.Is(
				err,
				ErrInvalidTransition,
			) {
				t.Fatalf("discard: %v", err)
			}
		})
	}
}

func TestDiscardReceiptRejectsRemoteLease(t *testing.T) {
	lease := remoteLeaseFixture()
	snapshot := taskForTransition(t).Snapshot()
	snapshot.AbandonedLaunches = []LaunchAbandonment{
		{
			Kind:          DiscardedWorker,
			WorkerRetired: true,
			Generation:    "gen-1",
			Lease:         lease,
			LeaseHolder:   lease.Holder,
			Project:       lease.Project,
			Worktree:      lease.Path,
			Branch:        lease.Branch,
			BaseCommit:    lease.BaseCommit,
		},
	}
	if _, err := NewMigrated(snapshot); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("remote discard receipt accepted: %v", err)
	}
}

func TestDiscardReceiptPreservesReturnedLease(t *testing.T) {
	original := runningTask(t)
	lease := original.Snapshot().Worker.Lease
	discarded, err := original.DiscardWorker("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, found := discarded.RetiredLease("gen-1"); !found || got != lease {
		t.Fatalf("lost held lease: %+v", got)
	}
	returned, err := discarded.RecordLeaseReturned("gen-1", lease)
	if err != nil {
		t.Fatal(err)
	}
	data, err := returned.Encode()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	lease.State = LeaseReturned
	if got, found := loaded.RetiredLease("gen-1"); !found || got != lease {
		t.Fatalf("lost returned lease: %+v", got)
	}
}
