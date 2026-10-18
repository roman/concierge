package taskstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRemoteResultClaimsRequireSerializedReservation(t *testing.T) {
	ctx := context.Background()
	root := versionTwoRoot(t)
	if err := UpgradeVersionTwo(ctx, root, root+"-backup"); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := remoteResultTask(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("c", 40)
	if _, err := store.Update(ctx, "task-1", func(task Task) (Task, error) {
		return task.SelectRemoteResult("gen-1", commit, "holder")
	}); !errors.Is(err, ErrWorkerConflict) {
		t.Fatalf("ordinary update claimed primary result resources: %v", err)
	}
	if _, err := store.SelectRemoteResult(ctx, "task-1", "gen-1", commit, "holder"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "task-1", func(task Task) (Task, error) { return task.RequestRemoteResultLease("gen-1") }); err != nil {
		t.Fatal(err)
	}
	lease := WorktreeLease{
		ID:         "local-lease",
		Holder:     "holder",
		Project:    "/primary/repo",
		Path:       "/primary/pool/slot/repo",
		Branch:     "feat/task",
		BaseCommit: commit,
		State:      LeaseHeld,
	}
	if _, err := store.Update(ctx, "task-1", func(task Task) (Task, error) { return task.RecordRemoteResultLease("gen-1", lease) }); !errors.Is(
		err,
		ErrWorkerConflict,
	) {
		t.Fatalf("ordinary update captured a primary lease: %v", err)
	}
	if _, err := store.RecordRemoteResultLease(ctx, "task-1", "gen-1", lease); err != nil {
		t.Fatal(err)
	}
}

func TestPrimaryResultClaimsBlockLocalLaunchesAndSurviveRetirement(t *testing.T) {
	for _, phase := range []string{"selected", "ready", "retired", "returned"} {
		t.Run(phase, func(t *testing.T) {
			owner, err := remoteResultTask(
				t,
			).SelectRemoteResult("gen-1", strings.Repeat("c", 40), "holder")
			if err != nil {
				t.Fatal(err)
			}
			if phase != "selected" {
				owner, err = owner.RequestRemoteResultLease("gen-1")
				if err != nil {
					t.Fatal(err)
				}
				owner, err = owner.RecordRemoteResultLease(
					"gen-1",
					WorktreeLease{
						ID:         "local-lease",
						Holder:     "holder",
						Project:    "/primary/repo",
						Path:       "/primary/pool/slot/repo",
						Branch:     "feat/task",
						BaseCommit: strings.Repeat("c", 40),
						State:      LeaseHeld,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				owner, err = owner.ConfirmRemoteResult("gen-1")
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "retired" || phase == "returned" {
				owner, err = owner.AcceptResult("gen-1", Close, "2026-10-17")
				if err != nil {
					t.Fatal(err)
				}
				owner, err = owner.RetireResult("gen-1")
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "returned" {
				snapshot := owner.Snapshot()
				snapshot.Receipt.RemoteResult.Lease.State = LeaseReturned
				owner, err = NewMigrated(snapshot)
				if err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := NewQueued(
				Definition{ID: "task-2", Title: "Local work", Repo: "repo", Kind: "task"},
			)
			if err != nil {
				t.Fatal(err)
			}
			intent := launchForTest()
			intent.Project, intent.Branch = "/primary/repo", "feat/task"
			candidate, err = candidate.ReserveInitialLaunch(intent)
			if err != nil {
				t.Fatal(err)
			}
			err = checkWorkerIdentity([]Task{owner}, candidate)
			if phase == "returned" {
				if err != nil {
					t.Fatalf("returned result blocked a local launch: %v", err)
				}
			} else if !errors.Is(err, ErrWorkerConflict) {
				t.Fatalf("local launch claimed reserved result branch: %v", err)
			}
		})
	}
}

func TestRemoteResultSelectionRefusesExistingLocalAndRemoteClaims(t *testing.T) {
	candidate, err := remoteResultTask(
		t,
	).SelectRemoteResult("gen-1", strings.Repeat("c", 40), "candidate-holder")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"local launch", "remote result", "independent remote endpoint"} {
		t.Run(kind, func(t *testing.T) {
			snapshot := remoteResultTask(t).Snapshot()
			snapshot.Definition.ID = "task-2"
			snapshot.Worker.Name = "another-worker"
			snapshot.Worker.Machine = "machine-2"
			snapshot.Worker.Lease.Remote.Machine = "machine-2"
			owner, err := NewMigrated(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "local launch":
				owner, err = NewQueued(
					Definition{ID: "task-2", Title: "Local work", Repo: "repo", Kind: "task"},
				)
				if err != nil {
					t.Fatal(err)
				}
				intent := launchForTest()
				intent.Project, intent.Branch = "/primary/repo", "feat/task"
				owner, err = owner.ReserveInitialLaunch(intent)
			case "remote result":
				owner, err = owner.SelectRemoteResult(
					"gen-1",
					strings.Repeat("d", 40),
					"another-holder",
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = checkWorkerIdentity([]Task{owner}, candidate)
			if kind == "independent remote endpoint" {
				if err != nil {
					t.Fatalf("remote paths collided across machines: %v", err)
				}
			} else if !errors.Is(err, ErrWorkerConflict) {
				t.Fatalf("claimed an occupied primary branch: %v", err)
			}
		})
	}
}
