package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func remoteWorktreeRequest(t *testing.T) WorktreeRequest {
	t.Helper()
	_, location := remoteProjectFixture(t)
	return WorktreeRequest{
		Task:       "task-one",
		Location:   location,
		Holder:     "primary-task-holder",
		Branch:     "task/result",
		BaseCommit: strings.Repeat("a", 40),
	}
}

func TestRemoteWorktreeJournalRetainsAcquisitionAcrossReloads(t *testing.T) {
	request := remoteWorktreeRequest(t)
	ctx := context.Background()
	for _, phase := range []worktreePhase{worktreeReserved, worktreeAcquiring, worktreeAcquired, worktreeReady} {
		if err := withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
			record, err := journal.read()
			if err != nil {
				return err
			}
			if record == nil {
				record = &worktreeRecord{Format: 1, Request: request, Backend: taskstate.RemoteGit}
			}
			record.Phase = phase
			if phase == worktreeAcquired || phase == worktreeReady {
				remote := request.Location
				remote.Backend = record.Backend
				record.Lease = taskstate.WorktreeLease{Remote: remote, ID: "lease-one", Holder: request.Holder, Project: remote.ProjectPath(), Path: filepath.Join(remote.PoolRoot(), "slot", "repo"), Branch: request.Branch, BaseCommit: request.BaseCommit, State: taskstate.LeaseHeld}
			}
			return journal.save(*record)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		if record == nil || record.Phase != worktreeReady || record.Lease.Remote.PrimaryRoot != request.Location.PrimaryRoot {
			t.Fatal("lost original lease ownership")
		}
		record.Phase = worktreeAcquiring
		if journal.save(*record) == nil {
			t.Fatal("allowed a ready lease to acquire again")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteWorktreeJournalRejectsChangedRequestsAndForeignFiles(t *testing.T) {
	request := remoteWorktreeRequest(t)
	ctx := context.Background()
	if err := withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		return journal.save(worktreeRecord{Format: 1, Request: request, Backend: taskstate.RemoteGit, Phase: worktreeReserved})
	}); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Branch = "task/another"
	if err := withWorktreeJournal(ctx, changed, func(journal *worktreeJournal) error { _, err := journal.read(); return err }); err == nil {
		t.Fatal("accepted a changed acquisition request")
	}
	if err := withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		if err := journal.root.Remove(journal.name); err != nil {
			return err
		}
		if err := journal.root.Symlink("../machine-owner", journal.name); err != nil {
			return err
		}
		if _, err := journal.read(); err == nil {
			t.Fatal("followed a foreign journal link")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(request.Location.Base, "machine-owner"))
	if err != nil || string(data) != "machine-1\n" {
		t.Fatal("modified unrelated ownership metadata")
	}
}
