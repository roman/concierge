package remoteworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/worktree"
)

func TestProjectGitDisablesFsmonitor(t *testing.T) {
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	manager := Worktrees{Git: worktree.Client{Executable: executable}}
	got, err := manager.projectGit(t.Context(), t.TempDir(), "config", "--get", "core.fsmonitor")
	if err != nil || got != "false" {
		t.Fatalf("effective core.fsmonitor = %q: %v", got, err)
	}
}

func TestReturnStopsFsmonitorAfterIdentityBeforeProcessChecks(t *testing.T) {
	for _, condition := range []string{"running", "not running", "other error", "wrong HEAD", "wrong lease"} {
		t.Run(condition, func(t *testing.T) {
			manager, request := remoteReturnFixture(t)
			remoteProjectGit(
				t,
				manager.Git.Executable,
				request.Lease.Project,
				"config",
				"core.fsmonitor",
				"true",
			)
			marker := filepath.Join(t.TempDir(), "stopped")
			wrapper := filepath.Join(t.TempDir(), "git")
			stop := "touch '" + marker + "'; exit 0"
			if condition == "not running" {
				stop = "touch '" + marker + "'; echo 'fatal: fsmonitor--daemon is not running' >&2; exit 128"
			}
			if condition == "other error" {
				stop = "echo 'fatal: permission denied' >&2; exit 128"
			}
			script := "#!/bin/sh\ncase \"$*\" in *'fsmonitor--daemon stop'*) " + stop + ";; esac\nexec '" + manager.Git.Executable + "' \"$@\"\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			manager.Git.Executable = wrapper
			if condition == "wrong HEAD" {
				request.Proof.Outcome = WorktreeLanded
				request.Proof.Commit = strings.Repeat("e", 40)
			}
			if condition == "wrong lease" {
				request.Lease.ID = "foreign"
			}
			checks := 0
			manager.CheckQuiet = func(context.Context, string) error {
				checks++
				if _, err := os.Stat(marker); err != nil {
					t.Fatal("process checks ran before fsmonitor stopped")
				}
				return nil
			}
			_, err := manager.Return(t.Context(), request)
			wantSuccess := condition == "running" || condition == "not running"
			if (err == nil) != wantSuccess || (checks > 0) != wantSuccess {
				t.Fatalf("return checks=%d: %v", checks, err)
			}
			if strings.HasPrefix(condition, "wrong") {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("stopped fsmonitor before confirming ownership and HEAD")
				}
			}
		})
	}
}
