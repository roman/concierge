package hostservice

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRecoveryRetiresStaleLaunchOnce(t *testing.T) {
	for _, scenario := range []string{"missing workspace", "held", "missing task", "unavailable inventory", "live launch"} {
		t.Run(scenario, func(t *testing.T) {
			server, input := launchRequestFixture(t)
			if err := os.Remove(filepath.Join(server.config.Root, "records", "task-1.json")); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			task, err := server.reserveInitialLaunch(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			generation := task.Snapshot().LaunchIntent.Generation
			t.Setenv("STALE_LAUNCH_SCENARIO", scenario)
			t.Setenv("STALE_LAUNCH_LABEL", task.Snapshot().LaunchIntent.Label)
			for _, advance := range []func(taskstate.Task) (taskstate.Task, error){
				func(task taskstate.Task) (taskstate.Task, error) { return task.BeginLaunchPreparation(generation) },
				func(task taskstate.Task) (taskstate.Task, error) { return task.RequestLaunchPane(generation) },
			} {
				if _, err := server.store.Update(ctx, input.TaskID, advance); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "held" {
				if _, err := server.store.Update(ctx, input.TaskID, func(task taskstate.Task) (taskstate.Task, error) {
					return task.PlaceHold(taskstate.Hold{Reason: "superseded"})
				}); err != nil {
					t.Fatal(err)
				}
			}
			git := filepath.Join(t.TempDir(), "git")
			if err := os.WriteFile(git, []byte("#!/bin/sh\ncase \"$3\" in\ncheck-ref-format) exit 0 ;;\ncat-file) printf 'commit\\n'; exit 0 ;;\nesac\nexit 72\n"), 0700); err != nil {
				t.Fatal(err)
			}
			server.config.GitExecutable = git
			script := `#!/bin/sh
case "$1 $2" in
'tab list') printf '%s\n' '{"error":{"code":"workspace_not_found","message":"workspace gone"}}'; exit 1 ;;
'workspace list')
 case "$STALE_LAUNCH_SCENARIO" in
 'unavailable inventory') printf '{}';;
 'live launch') printf '{"result":{"workspaces":[{"workspace_id":"other","label":"%s"}]}}\n' "$STALE_LAUNCH_LABEL" ;;
 *) printf '%s\n' '{"result":{"workspaces":[]}}' ;;
 esac ;;
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
*) exit 72 ;;
esac
`
			if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			server.config.Diagnostics = &diagnostics
			if scenario == "missing task" {
				if err := server.controllers.awaitInitialWakeup(controllerKey{kind: taskController, id: input.TaskID}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(server.config.Root, "records", input.TaskID+".json")); err != nil {
					t.Fatal(err)
				}
			} else if err := server.restoreTaskControllers(ctx); err != nil {
				t.Fatal(err)
			}
			for range 5 {
				server.reconcileInitialTasks(ctx)
			}
			if scenario == "unavailable inventory" || scenario == "live launch" {
				task, err = server.store.Read(ctx, input.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if task.Snapshot().LaunchIntent == nil ||
					len(task.Snapshot().AbandonedLaunches) != 0 {
					t.Fatal("abandoned an uncertain or live launch")
				}
				if len(server.controllers.initialWakeupTasks()) != 1 {
					t.Fatal("lost unresolved recovery")
				}
				return
			}
			if len(server.controllers.initialWakeupTasks()) != 0 {
				t.Fatalf("stale recovery remains pending: %s", &diagnostics)
			}
			if scenario != "missing task" {
				task, err = server.store.Read(ctx, input.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := task.LaunchAbandonment(generation); !ok ||
					task.Snapshot().LaunchIntent != nil {
					t.Fatal("missing terminal abandonment")
				}
				if err := server.restoreTaskControllers(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Count(diagnostics.String(), "\n") != 1 ||
				!strings.Contains(diagnostics.String(), "retired") {
				t.Fatalf("terminal diagnostic: %s", &diagnostics)
			}
		})
	}
}
