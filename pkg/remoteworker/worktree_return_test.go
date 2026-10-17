package remoteworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func remoteReturnFixture(t *testing.T) (Worktrees, WorktreeReturnRequest) {
	t.Helper()
	manager, location := remoteProjectFixture(t)
	base := remoteProjectGit(
		t,
		manager.Git.Executable,
		filepath.Join(location.Base, "repositories", "repo.git"),
		"rev-parse",
		"HEAD",
	)
	request := WorktreeRequest{
		Task:       "task-one",
		Location:   location,
		Holder:     "primary-task-holder",
		Branch:     "task/result",
		BaseCommit: base,
	}
	lease, err := manager.Acquire(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manager.CheckQuiet = func(context.Context, string) error { return nil }
	return manager, WorktreeReturnRequest{
		Worktree: request,
		Lease:    lease,
		Proof:    WorktreeReturnProof{Outcome: WorktreeUnchanged, Commit: base},
	}
}

func TestRemoteGitReturnRequiresQuietCleanOwnedCheckoutAndCommit(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	ctx := context.Background()
	manager.CheckQuiet = func(context.Context, string) error { return ErrWorktreeBusy }
	if _, err := manager.Return(ctx, request); !errors.Is(err, ErrWorktreeBusy) {
		t.Fatalf("busy return: %v", err)
	}
	manager.CheckQuiet = func(context.Context, string) error { return nil }
	file := filepath.Join(request.Lease.Path, "unfinished")
	if err := os.WriteFile(file, []byte("retain this work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Return(ctx, request); err == nil {
		t.Fatal("removed an unfinished checkout")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "retain this work" {
		t.Fatal("failed return changed the worker's file")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Proof = WorktreeReturnProof{
		Outcome: WorktreeLanded,
		Commit:  request.Worktree.Location.ClientCommit,
	}
	if _, err := manager.Return(ctx, changed); err == nil {
		t.Fatal("accepted a different immutable commit")
	}
	returned, err := manager.Return(ctx, request)
	if err != nil || returned.State != taskstate.LeaseReturned {
		t.Fatalf("return: %+v %v", returned, err)
	}
	if _, err := os.Lstat(request.Lease.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("physical checkout remains: %v", err)
	}
	manager.CheckQuiet = func(context.Context, string) error { t.Fatal("rechecked a confirmed return"); return nil }
	if retried, err := manager.Return(ctx, request); err != nil || retried != returned {
		t.Fatalf("lost return confirmation: %+v %v", retried, err)
	}
	changed = request
	changed.Proof.Outcome = WorktreeLanded
	if _, err := manager.Return(ctx, changed); err == nil {
		t.Fatal("replaced the durable return proof")
	}
	if _, err := manager.Acquire(ctx, request.Worktree); err == nil {
		t.Fatal("reacquired a returned physical lease")
	}
	if commit := remoteProjectGit(t, manager.Git.Executable, request.Lease.Project, "rev-parse", "refs/heads/"+request.Lease.Branch); commit != request.Proof.Commit {
		t.Fatal("cleanup lost the result branch")
	}
}

func TestRemoteReturnRejectsChangedLeaseAndInvalidProofBeforeProcessChecks(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	manager.CheckQuiet = func(context.Context, string) error { t.Fatal("invalid return reached a process check"); return nil }
	for _, mismatch := range []string{"lease path", "lease ID", "primary root", "mutable commit", "outcome", "unchanged commit"} {
		t.Run(mismatch, func(t *testing.T) {
			changed := request
			switch mismatch {
			case "lease path":
				changed.Lease.Path = request.Worktree.Location.PoolRoot() + "/foreign"
			case "lease ID":
				changed.Lease.ID = "different-lease"
			case "primary root":
				changed.Worktree.Location.PrimaryRoot = "/foreign-primary"
			case "mutable commit":
				changed.Proof.Commit = "HEAD"
			case "outcome":
				changed.Proof.Outcome = "unreviewed"
			case "unchanged commit":
				changed.Proof.Commit = request.Worktree.Location.ClientCommit
			}
			if _, err := manager.Return(context.Background(), changed); !errors.Is(
				err,
				ErrWorktreeOwnership,
			) {
				t.Fatalf("changed %s: %v", mismatch, err)
			}
		})
	}
	if _, err := os.Stat(request.Lease.Path); err != nil {
		t.Fatal("invalid return removed the owned checkout")
	}
}

func TestRemoteGitReturnRecoversAfterPhysicalRemoval(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	ctx := context.Background()
	if err := withWorktreeJournal(ctx, request.Worktree, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		record.Phase, record.ReturnProof = worktreeReturning, request.Proof
		return journal.save(*record)
	}); err != nil {
		t.Fatal(err)
	}
	remoteProjectGit(
		t,
		manager.Git.Executable,
		request.Lease.Project,
		"worktree",
		"remove",
		"--",
		request.Lease.Path,
	)
	manager.CheckQuiet = func(context.Context, string) error { t.Fatal("probed an absent checkout"); return nil }
	returned, err := manager.Return(ctx, request)
	if err != nil || returned.State != taskstate.LeaseReturned {
		t.Fatalf("interrupted physical removal: %+v %v", returned, err)
	}
}
