package conciergecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestHostLifecycleDuringStartupLaunchRecovery(t *testing.T) {
	for _, name := range []string{"CONCIERGE_WORKER_SOCKET", "CONCIERGE_WORKER_SECRET_FILE", "CONCIERGE_WORKER_TASK"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "concierge")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/concierge")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s, %v", data, err)
	}
	for _, operation := range []string{"stop", "restart"} {
		t.Run(operation, func(t *testing.T) {
			root := shortTaskRoot(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			if err := taskstate.Initialize(ctx, root); err != nil {
				t.Fatal(err)
			}
			store, err := taskstate.NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			task, err := taskstate.NewQueued(taskstate.Definition{
				ID: "task-1", Title: "Restore", Repo: "repo", Kind: "task",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Create(ctx, task); err != nil {
				t.Fatal(err)
			}
			worktree := t.TempDir()
			task, err = store.ReserveInitialLaunch(ctx, task.ID(), taskstate.LaunchIntent{
				Generation: "generation", Name: "worker", Label: "worker", Harness: "codex", Session: "work",
				Project: directory, Branch: "task", BaseCommit: strings.Repeat("a", 40), Body: "work",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Update(ctx, task.ID(), func(task taskstate.Task) (taskstate.Task, error) {
				return task.BeginLaunchPreparation("generation")
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.RequestLaunchLease(ctx, task.ID(), "generation", "holder"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.RecordLaunchLease(ctx, task.ID(), "generation", taskstate.WorktreeLease{
				ID: "lease-1", Holder: "holder", Project: directory, Path: worktree,
				Branch: "task", BaseCommit: strings.Repeat("a", 40), State: taskstate.LeaseHeld,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Update(ctx, task.ID(), func(task taskstate.Task) (taskstate.Task, error) {
				return task.RequestLaunchPane("generation")
			}); err != nil {
				t.Fatal(err)
			}
			task, err = store.RecordLaunchEndpoint(
				ctx,
				task.ID(),
				"generation",
				taskstate.LaunchEndpoint{
					Workspace: "gone-workspace", Tab: "gone-tab", Pane: "gone-pane",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			before := task.Snapshot()
			tools := t.TempDir()
			started := filepath.Join(tools, "started")
			t.Setenv("RECOVERY_CONTROL_STARTED", started)
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			for name, script := range map[string]string{
				"git": `#!/bin/sh
printf 'started\n' >> "$RECOVERY_CONTROL_STARTED"
exec sleep 120
`,
				"herdr": `#!/bin/sh
case "$1 $2" in
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`,
				"tincan":    "#!/bin/sh\nexit 0\n",
				"treehouse": "#!/bin/sh\nexit 0\n",
			} {
				if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--root", root, "--message-root", filepath.Join(root, "messages"),
				"host", "--session", "work", "--herdr", filepath.Join(tools, "herdr")}
			old := exec.Command(binary, append(args, "serve")...)
			log, err := os.Create(filepath.Join(tools, "host.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			old.Stdout, old.Stderr = log, log
			if err := old.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = old.Process.Signal(syscall.SIGTERM)
				done := make(chan error, 1)
				go func() { done <- old.Wait() }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = old.Process.Kill()
					<-done
					t.Error("fixture host did not stop on cancellation")
				}
			})
			waitForAttempts := func(count int) {
				t.Helper()
				for {
					data, _ := os.ReadFile(started)
					if strings.Count(string(data), "started\n") >= count {
						return
					}
					select {
					case <-ctx.Done():
						data, _ := os.ReadFile(log.Name())
						t.Fatalf("recovery did not start: %s", data)
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			waitForAttempts(1)
			if operation == "restart" {
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					data, err := exec.CommandContext(cleanup, binary, append(args, "stop")...).
						CombinedOutput()
					if err != nil {
						t.Errorf("replacement cleanup: %s, %v", data, err)
					}
				})
			}
			data, err := exec.CommandContext(ctx, binary, append(args, operation, "--timeout", "5s")...).
				CombinedOutput()
			if err != nil || !strings.Contains(string(data), "status: "+operation) {
				t.Fatalf("%s during startup recovery: %s, %v", operation, data, err)
			}
			if operation == "restart" {
				waitForAttempts(2)
			}
			after, err := store.Read(ctx, task.ID())
			if err != nil || !reflect.DeepEqual(before, after.Snapshot()) {
				t.Fatalf("%s changed retained intent: %+v, %v", operation, after.Snapshot(), err)
			}
			if _, err := os.Stat(worktree); err != nil {
				t.Fatalf("%s removed worktree: %v", operation, err)
			}
		})
	}
}
