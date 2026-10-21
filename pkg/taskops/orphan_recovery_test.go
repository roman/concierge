package taskops

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestRecoverOrphanedRequiresAbsenceAndRetainsProduct(t *testing.T) {
	for _, mode := range []string{"absent", "live pane", "live agent", "live process", "uncertain", "workspace", "lease", "generation"} {
		t.Run(mode, func(t *testing.T) {
			service, _, config, _ := pooledLaunchFixture(t)
			launched, err := service.ContinueInitialLaunch(t.Context(), "task-1", "gen-1", config)
			if err != nil {
				t.Fatal(err)
			}
			worker := launched.Snapshot().Worker
			commit := worker.Lease.BaseCommit
			if mode == "absent" {
				output, err := exec.Command(config.Git.Executable, "-C", worker.Worktree, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Preserved result").
					CombinedOutput()
				if err != nil {
					t.Fatalf("commit: %s %v", output, err)
				}
				output, err = exec.Command(config.Git.Executable, "-C", worker.Worktree, "rev-parse", "HEAD").
					Output()
				if err != nil {
					t.Fatal(err)
				}
				commit = strings.TrimSpace(string(output))
			}
			service.pool = config.Pool
			before, err := service.store.Update(
				t.Context(),
				"task-1",
				func(task taskstate.Task) (taskstate.Task, error) {
					return task.ReserveLaunch("gen-2")
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			product := filepath.Join(worker.Worktree, "unfinished")
			if err := os.WriteFile(product, []byte("preserve this work"), 0600); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
case "$1 $2" in
'pane get') printf '%s\n' '{"error":{"code":"pane_not_found"}}'; exit 1 ;;
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`
			generation := "gen-1"
			switch mode {
			case "live pane":
				script = "#!/bin/sh\nprintf '%s\\n' '{\"result\":{\"pane\":{\"pane_id\":\"" + worker.Pane + "\"}}}'\n"
			case "live agent":
				script = strings.Replace(
					script,
					"'{\"error\":{\"code\":\"agent_not_found\"}}'",
					"'{\"result\":{\"agent\":{\"pane_id\":\""+worker.Pane+"\"}}}'",
					1,
				)
			case "uncertain":
				script = "#!/bin/sh\nexit 72\n"
			case "workspace":
				script = strings.Replace(
					script,
					`{"result":{"workspaces":[]}}`,
					`{"result":{"workspaces":[{"workspace_id":"w1","label":"`+worker.OwnedWorkspaceLabel+`"}]}}`,
					1,
				)
			case "lease":
				service.pool.Executable = filepath.Join(t.TempDir(), "missing-pool")
			case "live process":
				status, err := json.Marshal(
					[]map[string]any{{"path": worker.Lease.Path, "lease_id": worker.Lease.ID,
						"lease_holder": worker.Lease.Holder, "flavor": "git", "processes": []map[string]int{{"pid": 123}}}},
				)
				if err != nil {
					t.Fatal(err)
				}
				statusFile := filepath.Join(t.TempDir(), "status.json")
				if err := os.WriteFile(statusFile, status, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CONCIERGE_ORPHAN_POOL_STATUS", statusFile)
				service.pool.Executable = filepath.Join(t.TempDir(), "pool")
				if err := os.WriteFile(service.pool.Executable, []byte("#!/bin/sh\ncat \"$CONCIERGE_ORPHAN_POOL_STATUS\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "generation":
				generation = "gen-2"
			}
			executable := filepath.Join(t.TempDir(), "herdr")
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			closed, err := service.RecoverOrphaned(
				t.Context(),
				"task-1",
				generation,
				"Superseded",
				"2026-10-20",
				herdr.NewClient(executable),
			)
			if mode != "absent" {
				if err == nil {
					t.Fatal("recovery accepted a live, uncertain, stale or changed worker")
				}
				after, readErr := service.store.Read(t.Context(), "task-1")
				if readErr != nil || !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
					t.Fatalf("refusal changed the pending task: %v", readErr)
				}
				return
			}
			if err != nil || closed.State() != taskstate.Done {
				t.Fatalf("recovery failed: %v", err)
			}
			if data, err := os.ReadFile(product); err != nil ||
				string(data) != "preserve this work" {
				t.Fatalf("recovery changed the worktree: %s %v", data, err)
			}
			if output, err := exec.Command(config.Git.Executable, "-C", worker.Worktree, "rev-parse", "HEAD").Output(); err != nil ||
				strings.TrimSpace(string(output)) != commit {
				t.Fatalf("recovery changed the result commit: %s %v", output, err)
			}
			lease, found, err := config.Pool.Find(t.Context(), worker.Project, worker.Lease.Holder)
			if err != nil || !found || lease.ID != worker.Lease.ID {
				t.Fatalf("recovery returned the lease: %v", err)
			}
			if _, err := service.RecoverOrphaned(t.Context(), "task-1", generation, "Superseded", "2026-10-20", herdr.Client{}); err != nil {
				t.Fatalf("durable retry inspected external resources: %v", err)
			}
		})
	}
}
