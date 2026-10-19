package remoteworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

func launchSiteFixture(t *testing.T) (Worktrees, LaunchSiteRequest) {
	t.Helper()
	short, err := os.MkdirTemp("/tmp", "cc-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	short, err = filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	manager, location := remoteProjectAt(t, short)
	bare := filepath.Join(short, "repositories", "repo.git")
	request := WorktreeRequest{
		Task:       "task-one",
		Location:   location,
		Holder:     "initial-launch-holder",
		Branch:     "task/result",
		BaseCommit: remoteProjectGit(t, manager.Git.Executable, bare, "rev-parse", "HEAD"),
	}
	lease, err := manager.Acquire(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return manager, LaunchSiteRequest{Worktree: request, Lease: lease, Generation: "generation-one"}
}

func TestRemoteLaunchSitePreparesPrivateDirectoryWithoutIssuingASecret(t *testing.T) {
	manager, request := launchSiteFixture(t)
	for range 2 {
		site, err := manager.PrepareLaunchSite(context.Background(), request)
		if err != nil || site.Lease != request.Lease || site.Environment != herdr.PlainWorktree {
			t.Fatalf("prepared site: %+v %v", site, err)
		}
		if info, err := os.Stat(site.Tools.TaskDirectory); err != nil ||
			info.Mode().Perm() != 0700 {
			t.Fatal("task directory is not private")
		}
		if _, err := os.Lstat(filepath.Join(site.Tools.TaskDirectory, "secret")); !os.IsNotExist(
			err,
		) {
			t.Fatal("issued a secret before an active grant")
		}
		if site.Tools.Concierge != request.Worktree.Location.ClientPath() {
			t.Fatal("changed the prepared client")
		}
	}
	site, err := manager.PrepareLaunchSite(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site.Tools.TaskDirectory, "retained-result"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	request.Generation = "generation-two"
	next, err := manager.PrepareLaunchSite(context.Background(), request)
	if err != nil || next.Tools.TaskDirectory == site.Tools.TaskDirectory {
		t.Fatal("reused a prior generation's task directory")
	}
	data, err := os.ReadFile(filepath.Join(site.Tools.TaskDirectory, "retained-result"))
	if err != nil || string(data) != "keep" {
		t.Fatal("discarded the prior result")
	}
}

func TestRemoteLaunchSiteRefusesForeignOwnerAndChangedLease(t *testing.T) {
	for _, kind := range []string{"owner", "lease", "generation"} {
		t.Run(kind, func(t *testing.T) {
			manager, request := launchSiteFixture(t)
			site, err := manager.PrepareLaunchSite(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "owner":
				if err := os.WriteFile(filepath.Join(site.Tools.TaskDirectory, "launch-owner.json"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "lease":
				request.Lease.ID = "another-lease"
			case "generation":
				request.Generation = ""
			}
			if _, err := manager.PrepareLaunchSite(context.Background(), request); err == nil {
				t.Fatal("prepared an unowned launch site")
			}
		})
	}
}

func TestRemoteLaunchSiteDetectsWorktreeEnvironmentAfterAcquisition(t *testing.T) {
	manager, request := launchSiteFixture(t)
	if err := os.WriteFile(filepath.Join(request.Lease.Path, ".envrc"), []byte("use flake\n"), 0600); err != nil {
		t.Fatal(err)
	}
	site, err := manager.PrepareLaunchSite(context.Background(), request)
	if err != nil || site.Environment != herdr.DirenvWorktree {
		t.Fatalf("worktree environment: %+v %v", site, err)
	}
}

func TestRemoteLaunchSiteRejectsUnclaimedOrSymbolicDirectory(t *testing.T) {
	for _, kind := range []string{"unclaimed", "symbolic"} {
		t.Run(kind, func(t *testing.T) {
			manager, request := launchSiteFixture(t)
			tools, err := request.Tools()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Dir(tools.TaskDirectory), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "unclaimed" {
				if err := os.Mkdir(tools.TaskDirectory, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(t.TempDir(), tools.TaskDirectory); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.PrepareLaunchSite(context.Background(), request); err == nil {
				t.Fatal("claimed an unrelated directory")
			}
		})
	}
}

func TestRemoteLaunchSiteReportsTheDarwinSocketPathLimit(t *testing.T) {
	_, request := launchSiteFixture(t)
	request.Worktree.Location.Base = "/" + strings.Repeat("b", 120)
	request.Lease.Remote.Base = request.Worktree.Location.Base
	request.Lease.Project = request.Lease.Remote.ProjectPath()
	request.Lease.Path = filepath.Join(request.Lease.Remote.PoolRoot(), "slot", "repo")
	if _, err := request.Tools(); !errors.Is(err, ErrLaunchSocketPath) ||
		!strings.Contains(err.Error(), "103 bytes") {
		t.Fatalf("long base needs an exact operator step: %v", err)
	}
}

func TestRemoteToolsAgreeBeforeAndAfterLeaseCapture(t *testing.T) {
	_, request := launchSiteFixture(t)
	before, err := ToolsForGeneration(
		request.Worktree.Location,
		request.Worktree.Task,
		request.Generation,
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := request.Tools()
	if err != nil || before != after {
		t.Fatal("changed native paths after lease capture")
	}
	restarted, err := ToolsForGeneration(
		request.Lease.Remote,
		request.Worktree.Task,
		request.Generation,
	)
	if err != nil || restarted != before {
		t.Fatal("changed native paths for the captured backend")
	}
	if _, err := ToolsForGeneration(request.Worktree.Location, request.Worktree.Task, ""); err == nil {
		t.Fatal("derived paths without a generation")
	}
}
