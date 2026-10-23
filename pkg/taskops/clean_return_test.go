package taskops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestReturnCleanupRequiresAcceptanceLandingAndQuietLease(t *testing.T) {
	for _, condition := range []string{"landed", "unlanded", "unaccepted", "busy", "scout", "fsmonitor", "fsmonitor not running", "fsmonitor unsupported", "fsmonitor error"} {
		t.Run(condition, func(t *testing.T) {
			service, _, config, _ := pooledLaunchFixture(t)
			launched, err := service.ContinueInitialLaunch(t.Context(), "task-1", "gen-1", config)
			if err != nil {
				t.Fatal(err)
			}
			worker := launched.Snapshot().Worker
			run := func(args ...string) {
				t.Helper()
				if output, err := exec.Command(config.Git.Executable, append([]string{"-C", worker.Worktree}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %s %v", output, err)
				}
			}
			run("config", "core.fsmonitor", "false")
			run(
				"-c",
				"user.name=Test",
				"-c",
				"user.email=test@example.invalid",
				"commit",
				"--allow-empty",
				"-m",
				"Result",
			)
			if condition != "unlanded" && condition != "scout" {
				run("push", "origin", "HEAD:main")
			}
			if condition == "scout" {
				if _, err := service.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
					snapshot := task.Snapshot()
					snapshot.Definition.Kind = "scout"
					return taskstate.NewMigrated(snapshot)
				}); err != nil {
					t.Fatal(err)
				}
			}
			monitorStopped := filepath.Join(t.TempDir(), "monitor-stopped")
			if strings.HasPrefix(condition, "fsmonitor") {
				run("config", "core.fsmonitor", "true")
				wrapper := filepath.Join(t.TempDir(), "git")
				stop := "touch '" + monitorStopped + "'; exit 0"
				if condition == "fsmonitor not running" {
					stop = "echo 'fatal: fsmonitor--daemon is not running' >&2; exit 128"
				}
				if condition == "fsmonitor unsupported" {
					stop = "echo 'fatal: fsmonitor--daemon not supported on this platform' >&2; exit 128"
				}
				if condition == "fsmonitor error" {
					stop = "echo 'fatal: permission denied' >&2; exit 128"
				}
				script := "#!/bin/sh\ncase \"$*\" in *'fsmonitor--daemon stop'*) " + stop + ";; esac\nexec '" + config.Git.Executable + "' \"$@\"\n"
				if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				config.Git.Executable = wrapper
			}
			if err := os.WriteFile(filepath.Join(worker.Project, ".git", "info", "exclude"), []byte("ignored\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"ignored", "untracked"} {
				if err := os.WriteFile(filepath.Join(worker.Worktree, name), []byte("output"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			service, err = NewWithWorkerTools(
				service.root,
				service.primaryRecipient,
				WorkerTools{Commands: service.workerCommands, Pool: config.Pool, Git: config.Git},
			)
			if err != nil {
				t.Fatal(err)
			}
			if condition != "unaccepted" {
				if _, err := service.Finish(t.Context(), "task-1", "gen-1", taskstate.Close, "2026-10-23"); err != nil {
					t.Fatal(err)
				}
			}
			var process *exec.Cmd
			if condition == "busy" {
				process = exec.Command("sleep", "60")
				process.Dir = worker.Worktree
				if err := process.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
			}
			runner := &retirementRunner{
				pane:     worker.Pane,
				response: `{"error":{"code":"pane_not_found"}}`,
			}
			_, err = service.Retire(
				t.Context(),
				"task-1",
				"gen-1",
				herdr.Client{SessionRunner: runner},
			)
			wantClean := condition == "landed" || condition == "scout" ||
				condition == "fsmonitor" ||
				condition == "fsmonitor not running" ||
				condition == "fsmonitor unsupported"
			if (err == nil) != wantClean {
				t.Fatalf("return: %v", err)
			}
			if condition == "busy" && !strings.Contains(err.Error(), "pid=") {
				t.Fatalf("lost process diagnostics: %v", err)
			}
			if condition == "fsmonitor" {
				if _, err := os.Stat(monitorStopped); err != nil {
					t.Fatalf("checkout returned before fsmonitor stopped: %v", err)
				}
			}
			for _, name := range []string{"ignored", "untracked"} {
				_, err := os.Stat(filepath.Join(worker.Worktree, name))
				if os.IsNotExist(err) != wantClean {
					t.Fatalf("%s: %v", name, err)
				}
			}
			_, held, err := config.Pool.Find(t.Context(), worker.Project, worker.Lease.Holder)
			if err != nil || held == wantClean {
				t.Fatalf("held=%t: %v", held, err)
			}
		})
	}
}
