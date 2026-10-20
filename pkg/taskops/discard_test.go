package taskops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
)

func TestDiscardWorkerRequiresAbsenceAndPreservesProduct(t *testing.T) {
	for _, kind := range []string{"clean", "dirty", "ignored", "commit", "report", "pending reply", "generation", "live", "native live", "unknown", "lease", "return refusal"} {
		t.Run(kind, func(t *testing.T) {
			service, _, config, _ := pooledLaunchFixture(t)
			launched, err := service.ContinueInitialLaunch(t.Context(), "task-1", "gen-1", config)
			if err != nil {
				t.Fatal(err)
			}
			worker := launched.Snapshot().Worker
			service.pool, service.git = config.Pool, config.Git
			if err := taskview.Rebuild(t.Context(), service.root); err != nil {
				t.Fatal(err)
			}
			messages := t.TempDir()
			generation := "gen-1"
			script := `#!/bin/sh
case "$1 $2" in
'pane get') printf '%s\n' '{"error":{"code":"pane_not_found"}}'; exit 1 ;;
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`
			switch kind {
			case "dirty", "ignored":
				if kind == "ignored" {
					if err := os.WriteFile(filepath.Join(worker.Project, ".git", "info", "exclude"), []byte("product\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(worker.Worktree, "product"), []byte("unfinished"), 0600); err != nil {
					t.Fatal(err)
				}
			case "generation":
				generation = "gen-2"
			case "commit":
				output, err := exec.Command(config.Git.Executable, "-C", worker.Worktree, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Unfinished product").
					CombinedOutput()
				if err != nil {
					t.Fatalf("commit: %s: %v", output, err)
				}
			case "report", "pending reply":
				if _, err := service.SendReply(t.Context(), messages, ReplyInput{TaskID: "task-1", Generation: "gen-1", Key: "result", Body: "Product"}); err != nil {
					t.Fatal(err)
				}
				if kind == "pending reply" {
					if err := os.RemoveAll(filepath.Join(service.root, "reports")); err != nil {
						t.Fatal(err)
					}
				}
			case "live":
				script = "#!/bin/sh\nprintf '%s\\n' '{\"result\":{\"pane\":{\"pane_id\":\"" + worker.Pane + "\"}}}'\n"
			case "unknown":
				script = "#!/bin/sh\nexit 72\n"
			case "native live":
				script = strings.Replace(
					script,
					"'{\"error\":{\"code\":\"agent_not_found\"}}'",
					"'{\"result\":{\"agent\":{\"pane_id\":\""+worker.Pane+"\"}}}'",
					1,
				)
			case "lease":
				service.pool.Executable = filepath.Join(t.TempDir(), "refusing-pool")
			case "return refusal":
				t.Setenv("DISCARD_TEST_POOL", config.Pool.Executable)
				service.pool.Executable = filepath.Join(t.TempDir(), "pool")
				if err := os.WriteFile(service.pool.Executable, []byte("#!/bin/sh\nif [ \"$1\" = return ]; then echo 'ownership refusal' >&2; exit 72; fi\nexec \"$DISCARD_TEST_POOL\" \"$@\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			executable := filepath.Join(t.TempDir(), "herdr")
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			discarded, err := service.DiscardWorker(
				t.Context(),
				messages,
				"task-1",
				generation,
				herdr.NewClient(executable),
			)
			if kind != "clean" {
				if err == nil {
					t.Fatal("unsafe discard succeeded")
				}
				if kind == "dirty" || kind == "ignored" || kind == "commit" {
					var decision *DiscardDecisionRequired
					if !errors.As(err, &decision) || !decision.Inventory.RequiresDecision() {
						t.Fatalf("missing inventory: %v", err)
					}
				}
				current, readErr := service.store.Read(t.Context(), "task-1")
				if kind == "return refusal" {
					view, viewErr := os.ReadFile(filepath.Join(service.root, "backlog.md"))
					if viewErr != nil ||
						!strings.Contains(string(view), "## Queued\n- [ ] task-1") {
						t.Fatalf("discard refusal left stale projection: %s %v", view, viewErr)
					}
					receipt, found := current.LaunchAbandonment("gen-1")
					if readErr != nil || !found || receipt.Lease != worker.Lease ||
						current.Snapshot().Worker != nil ||
						!strings.Contains(err.Error(), "ownership refusal") {
						t.Fatalf(
							"return refusal lost durable lease: %+v %v",
							current.Snapshot(),
							err,
						)
					}
					service.pool = config.Pool
					if _, err := service.DiscardWorker(t.Context(), messages, "task-1", "gen-1", herdr.Client{}); err != nil {
						t.Fatalf("partial failure retry: %v", err)
					}
					return
				}
				if readErr != nil || current.Snapshot().Worker == nil ||
					len(current.Snapshot().AbandonedLaunches) != 0 {
					t.Fatalf("refusal lost binding: %v", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			receipt, found := discarded.LaunchAbandonment("gen-1")
			if !found || receipt.Kind != taskstate.DiscardedWorker ||
				receipt.Lease.State != taskstate.LeaseReturned ||
				discarded.Snapshot().Completion != nil ||
				discarded.Snapshot().Receipt != nil {
				t.Fatalf("discard: %+v", discarded.Snapshot())
			}
			if _, err := service.DiscardWorker(t.Context(), messages, "task-1", "gen-1", herdr.Client{}); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}
