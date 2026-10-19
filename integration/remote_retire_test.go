package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestAcceptanceTimeoutRetiresPartialLaunchAndHeldLease(t *testing.T) {
	for _, mode := range []string{"bound", "unbound", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			root := filepath.Join(directory, "state")
			if err := os.MkdirAll(filepath.Join(root, "records"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("3\n"), 0600); err != nil {
				t.Fatal(err)
			}
			lease := taskstate.WorktreeLease{
				ID:         "lease-one",
				Holder:     "holder",
				Project:    "/project",
				Path:       "/worktree",
				Branch:     "acceptance/task",
				BaseCommit: strings.Repeat("a", 40),
				State:      taskstate.LeaseHeld,
			}
			intent := &taskstate.LaunchIntent{
				Generation:  "gen-1",
				Harness:     "codex",
				Name:        "worker",
				Session:     "default",
				Project:     lease.Project,
				Worktree:    lease.Path,
				Branch:      lease.Branch,
				BaseCommit:  lease.BaseCommit,
				Label:       "concierge-gen-1",
				Lease:       lease,
				LeaseHolder: lease.Holder,
				Phase:       taskstate.LaunchLeaseReady,
			}
			snapshot := taskstate.Snapshot{
				Definition: taskstate.Definition{
					ID:    "task-1",
					Title: "Acceptance",
					Kind:  "scout",
					Repo:  "concierge",
				},
				State:        taskstate.Queued,
				LaunchIntent: intent,
			}
			if mode != "unbound" {
				intent.Phase = taskstate.LaunchAgentRequested
				intent.Endpoint = &taskstate.LaunchEndpoint{
					Workspace: "w1",
					Tab:       "w1:t1",
					Pane:      "w1:p1",
				}
				intent.ResumeArgs = []string{"resume", "native-id"}
				snapshot.State = taskstate.InFlight
				snapshot.Worker = &taskstate.Worker{
					Name:       intent.Name,
					Harness:    intent.Harness,
					Session:    intent.Session,
					Project:    intent.Project,
					Worktree:   intent.Worktree,
					Generation: intent.Generation,
					Workspace:  "w1",
					Tab:        "w1:t1",
					Pane:       "w1:p1",
					AgentArgs:  intent.ResumeArgs,
				}
			}
			original, err := taskstate.NewMigrated(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			abandoned, err := original.AbandonInitialLaunch(intent.Generation)
			if err != nil {
				t.Fatal(err)
			}
			returned, err := abandoned.RecordLeaseReturned(intent.Generation, lease)
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(root, "records", "task-1.json")
			for name, task := range map[string]taskstate.Task{record: original, filepath.Join(directory, "abandoned"): abandoned, filepath.Join(directory, "returned"): returned} {
				data, err := task.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for key, value := range map[string]string{"TEST_ACCEPTANCE_RECORD": record, "TEST_ACCEPTANCE_DIRECTORY": directory, "TEST_ACCEPTANCE_MODE": mode} {
				t.Setenv(key, value)
			}
			binary := filepath.Join(directory, "concierge")
			script := `#!/bin/sh
shift 6
printf '%s %s\n' "$1" "$2" >> "$TEST_ACCEPTANCE_DIRECTORY/calls"
case "$1 $2" in
'backlog hold') exit 0 ;;
'task abandon')
 [ "$TEST_ACCEPTANCE_MODE" != uncertain ] || exit 73
 selected=returned
 [ "$TEST_ACCEPTANCE_MODE" != unbound ] || selected=abandoned
 cp "$TEST_ACCEPTANCE_DIRECTORY/$selected" "$TEST_ACCEPTANCE_RECORD" ;;
'backlog release-worktree') cp "$TEST_ACCEPTANCE_DIRECTORY/returned" "$TEST_ACCEPTANCE_RECORD" ;;
*) exit 74 ;;
esac
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			store, err := taskstate.NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			run := &savedMachineAcceptance{
				t:         t,
				directory: directory,
				root:      root,
				messages:  filepath.Join(directory, "messages"),
				binary:    binary,
				store:     store,
			}
			err = run.retireAcceptanceTask(t.Context(), "task-1")
			current, readErr := store.Read(t.Context(), "task-1")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "uncertain" {
				if err == nil || current.Snapshot().Worker == nil ||
					current.Snapshot().LaunchIntent == nil {
					t.Fatalf("lost uncertain binding: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			receipt, found := current.LaunchAbandonment(intent.Generation)
			if !found || receipt.Lease.State != taskstate.LeaseReturned ||
				current.Snapshot().Worker != nil ||
				current.Snapshot().LaunchIntent != nil {
				t.Fatal("timeout retained launch resources")
			}
			if err := run.retireAcceptanceTask(t.Context(), "task-1"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
