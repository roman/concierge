package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func TestRemoteGitLeaseRetryPreservesWorkerChanges(t *testing.T) {
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
	if err != nil || lease.Remote.Backend != taskstate.RemoteGit ||
		!strings.HasPrefix(lease.Path, location.PoolRoot()+"/") {
		t.Fatalf("acquire: %+v %v", lease, err)
	}
	file := filepath.Join(lease.Path, "worker-result")
	if err := os.WriteFile(file, []byte("unfinished work"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		recovered, err := manager.Acquire(context.Background(), request)
		if err != nil || recovered != lease {
			t.Fatalf("recovered another lease: %+v %v", recovered, err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "unfinished work" {
		t.Fatal("discarded worker changes")
	}
	changed := request
	changed.Branch = "task/other"
	if _, err := manager.Acquire(context.Background(), changed); err == nil {
		t.Fatal("rebound the physical lease")
	}
}

func TestRemoteGitAcquisitionRecoversBeforeLeaseCapture(t *testing.T) {
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
	if err := withWorktreeJournal(context.Background(), request, func(journal *worktreeJournal) error {
		record := worktreeRecord{Format: 1, Request: request, Backend: taskstate.RemoteGit, Phase: worktreeReserved}
		if err := journal.save(record); err != nil {
			return err
		}
		record.Phase = worktreeAcquiring
		return journal.save(record)
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(context.Background(), request)
	if err != nil || lease.ID == "" {
		t.Fatalf("interrupted deterministic acquisition: %+v %v", lease, err)
	}
	if head := remoteProjectGit(t, manager.Git.Executable, lease.Path, "rev-parse", "HEAD"); head != base {
		t.Fatal("did not use the immutable base")
	}
}

func TestRemoteTreehouseRecoveryNeverAcquiresAnotherSlot(t *testing.T) {
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
	manager.Treehouse = filepath.Join(t.TempDir(), "treehouse")
	calls := filepath.Join(t.TempDir(), "acquisitions")
	script := `#!/bin/sh
[ "$1" = --root ] || exit 73
shift 2
case "$1" in
status) printf '[]' ;;
get) printf called > ` + sshcommand.Quote(calls) + `; exit 74 ;;
*) exit 75 ;;
esac
`
	if err := os.WriteFile(manager.Treehouse, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := withWorktreeJournal(context.Background(), request, func(journal *worktreeJournal) error {
		record := worktreeRecord{Format: 1, Request: request, Backend: taskstate.RemoteTreehouse, Phase: worktreeReserved}
		if err := journal.save(record); err != nil {
			return err
		}
		record.Phase = worktreeAcquiring
		return journal.save(record)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), request); err == nil {
		t.Fatal("replaced an unconfirmed Treehouse acquisition")
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("repeated an interrupted acquisition")
	}
	manager.Treehouse = ""
	if _, err := manager.Acquire(context.Background(), request); err == nil {
		t.Fatal("fell back to Git after selecting Treehouse")
	}
}

func TestRemoteTreehouseIntegration(t *testing.T) {
	executable := os.Getenv("TREEHOUSE_TEST_EXECUTABLE")
	if executable == "" {
		t.Skip("set TREEHOUSE_TEST_EXECUTABLE to the pinned treehouse binary")
	}
	manager, location := remoteProjectFixture(t)
	manager.Treehouse = executable
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
	if lease.Remote.Backend != taskstate.RemoteTreehouse {
		t.Fatal("did not use the available Treehouse")
	}
	file := filepath.Join(lease.Path, "result")
	if err := os.WriteFile(file, []byte("unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := manager.Acquire(context.Background(), request)
	if err != nil || recovered != lease {
		t.Fatalf("changed or rejected the active worker: %+v %v", recovered, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	pool := worktree.PoolClient{Executable: executable, Root: location.PoolRoot()}
	if err := pool.Return(context.Background(), lease.Project, worktree.Lease{ID: lease.ID, Holder: lease.Holder, Path: lease.Path}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteWorktreeUsesANewHolderForTheNextLaunch(t *testing.T) {
	manager, location := remoteProjectFixture(t)
	bare := filepath.Join(location.Base, "repositories", "repo.git")
	request := WorktreeRequest{
		Task:       "task-one",
		Location:   location,
		Holder:     "first-launch",
		Branch:     "task/first",
		BaseCommit: remoteProjectGit(t, manager.Git.Executable, bare, "rev-parse", "HEAD"),
	}
	first, err := manager.Acquire(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	remoteProjectGit(t, manager.Git.Executable, first.Project, "worktree", "remove", first.Path)
	if err := withWorktreeJournal(context.Background(), request, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		record.Phase = worktreeReturning
		if err := journal.save(*record); err != nil {
			return err
		}
		record.Phase = worktreeReturned
		record.Lease.State = taskstate.LeaseReturned
		return journal.save(*record)
	}); err != nil {
		t.Fatal(err)
	}
	request.Holder = "next-launch"
	request.Branch = "task/next"
	next, err := manager.Acquire(context.Background(), request)
	if err != nil || next.ID == first.ID || next.Path == first.Path {
		t.Fatalf("reused or rejected another launch lifetime: %+v %v", next, err)
	}
}
