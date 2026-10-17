package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestNativeInspectionRecoversAnExistingLeaseWithoutAcquiringOrPreparing(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	ctx := context.Background()
	manager.CheckQuiet = func(context.Context, string) error { t.Fatal("inspection reached cleanup"); return nil }
	file := filepath.Join(request.Lease.Path, "unfinished")
	if err := os.WriteFile(file, []byte("preserve the worker's work"), 0600); err != nil {
		t.Fatal(err)
	}
	before := remoteProjectGit(
		t,
		manager.Git.Executable,
		request.Lease.Project,
		"worktree",
		"list",
		"--porcelain",
		"-z",
	)
	got, err := manager.Inspect(ctx, request.Worktree)
	if err != nil || got != request.Lease {
		t.Fatalf("recovered native lease: %+v %v", got, err)
	}
	changed := request.Worktree
	changed.Holder = "different-holder"
	if _, err := manager.Inspect(ctx, changed); err == nil {
		t.Fatal("inspection acquired an unknown lease")
	}
	if after := remoteProjectGit(t, manager.Git.Executable, request.Lease.Project, "worktree", "list", "--porcelain", "-z"); after != before {
		t.Fatal("inspection changed the registered worktrees")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "preserve the worker's work" {
		t.Fatal("inspection changed unfinished work")
	}
	location := request.Worktree.Location
	profile := herdr.SavedMachine{ID: location.Machine, Target: location.Target, Enabled: true}
	fixture := &worktreeSSHFixture{
		t:         t,
		request:   request.Worktree,
		response:  request.Lease,
		operation: "inspect",
	}
	transport := WorktreeTransport{Runner: fixture}
	if captured, err := transport.Inspect(ctx, profile, request.Worktree); err != nil ||
		captured != request.Lease {
		t.Fatalf("fixed SSH inspection: %+v %v", captured, err)
	}
	fixture.response.State = taskstate.LeaseReturned
	if _, err := transport.Inspect(ctx, profile, request.Worktree); err == nil {
		t.Fatal("recaptured a returned lease as held")
	}
}
