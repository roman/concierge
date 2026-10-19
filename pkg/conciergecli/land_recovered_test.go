package conciergecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func recoveredLandingFixture(t *testing.T) (landingFixture, string) {
	t.Helper()
	f := newLandingFixture(t)
	lease := f.worker.Lease
	lease.State = taskstate.LeaseReturned
	receipt := taskstate.Receipt{
		Generation: f.worker.Generation,
		Mode:       taskstate.Close,
		Lease:      lease,
	}
	task, err := taskstate.NewMigrated(taskstate.Snapshot{
		Definition: taskstate.Definition{ID: "task-1", Title: "Work", Repo: "repo", Kind: "audit"},
		State:      taskstate.Done, Receipt: &receipt,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	poolStatus := filepath.Join(t.TempDir(), "status.json")
	writeRecoveredPoolStatus(t, poolStatus, f.worktree, "", "")
	t.Setenv("CONCIERGE_TEST_POOL_STATUS", poolStatus)
	t.Setenv("CONCIERGE_TEST_POOL_PATH", f.worktree)
	pool := filepath.Join(t.TempDir(), "treehouse")
	script := `#!/bin/sh
case "$1" in
status) cat "$CONCIERGE_TEST_POOL_STATUS" ;;
lease)
  [ -z "$CONCIERGE_TEST_LEASE_FAIL" ] || exit 78
  processes='[]'
  [ -z "$CONCIERGE_TEST_LEASE_BUSY" ] || processes='[{"pid":123}]'
  printf '[{"name":"slot-1","path":"%s","lease_id":"recovery-lease","lease_holder":"%s","flavor":"git","processes":%s}]\n' "$CONCIERGE_TEST_POOL_PATH" "$5" "$processes" > "$CONCIERGE_TEST_POOL_STATUS"
  response_path="$CONCIERGE_TEST_POOL_PATH"
  [ -z "$CONCIERGE_TEST_WRONG_PATH" ] || response_path="$CONCIERGE_TEST_WRONG_PATH"
  printf '{"path":"%s","lease_id":"recovery-lease","lease_holder":"%s"}\n' "$response_path" "$5"
  ;;
return)
  [ -z "$CONCIERGE_TEST_RETURN_FAIL" ] || exit 80
  printf '[{"name":"slot-1","path":"%s","lease_id":"","lease_holder":"","flavor":"git","processes":[]}]\n' "$CONCIERGE_TEST_POOL_PATH" > "$CONCIERGE_TEST_POOL_STATUS"
  ;;
*) exit 79 ;;
esac
`
	if err := os.WriteFile(pool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return f, pool
}

func writeRecoveredPoolStatus(t *testing.T, path, worktree, id, holder string) {
	t.Helper()
	data, err := json.Marshal([]map[string]any{{
		"name": "slot-1", "path": worktree, "lease_id": id, "lease_holder": holder,
		"flavor": "git", "processes": []any{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func recoveredLandingArgs(f landingFixture, pool string) []string {
	return []string{"land-recovered", "task-1", "--generation", "gen-1", "--commit", f.head,
		"--treehouse", pool, "--operator-approved"}
}

func changeRecoveredTask(t *testing.T, root string, change func(*taskstate.Snapshot)) {
	t.Helper()
	store, err := taskstate.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := current.Snapshot()
	change(&snapshot)
	updated, err := taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := updated.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredLandingRequiresExactReceiptAndApproval(t *testing.T) {
	f, pool := recoveredLandingFixture(t)
	base := landingGit(t, f.remote, "rev-parse", "trunk")
	for _, change := range []struct {
		name string
		args func([]string) []string
	}{
		{"approval", func(args []string) []string { return args[:len(args)-1] }},
		{"generation", func(args []string) []string { args[3] = "other"; return args }},
		{"commit", func(args []string) []string { args[5] = base; return args }},
	} {
		t.Run(change.name, func(t *testing.T) {
			args := change.args(recoveredLandingArgs(f, pool))
			expectLifecycleError(t, f.root, 1, args...)
			if got := landingGit(t, f.remote, "rev-parse", "trunk"); got != base {
				t.Fatal("rejected result changed the remote")
			}
		})
	}
	got := invoke(t, f.root, "", recoveredLandingArgs(f, pool)...)
	if !strings.Contains(got, "commit: "+quote(f.head)) {
		t.Fatal(got)
	}
	if landed := landingGit(t, f.remote, "rev-parse", "trunk"); landed != f.head {
		t.Fatal("selected commit did not land")
	}
}

func TestRecoveredLandingResumesItsOwnReservation(t *testing.T) {
	f, pool := recoveredLandingFixture(t)
	holder := "concierge-land-recovered:task-1:gen-1:" + f.head
	writeRecoveredPoolStatus(
		t,
		os.Getenv("CONCIERGE_TEST_POOL_STATUS"),
		f.worktree,
		"recovery-lease",
		holder,
	)
	invoke(t, f.root, "", recoveredLandingArgs(f, pool)...)
	if landed := landingGit(t, f.remote, "rev-parse", "trunk"); landed != f.head {
		t.Fatal("reserved result did not land")
	}
	data, err := os.ReadFile(os.Getenv("CONCIERGE_TEST_POOL_STATUS"))
	if err != nil || strings.Contains(string(data), "recovery-lease") {
		t.Fatalf("reservation remains after landing: %s, %v", data, err)
	}
}

func TestRecoveredLandingRecordsCheckoutAfterReleaseFailure(t *testing.T) {
	f, pool := recoveredLandingFixture(t)
	t.Setenv("CONCIERGE_TEST_RETURN_FAIL", "1")
	writeLandingFile(t, f.project, "tracked", "local change")
	output := expectLifecycleError(t, f.root, 1, recoveredLandingArgs(f, pool)...)
	if !strings.Contains(output, "landed") {
		t.Fatal(output)
	}
	if landed := landingGit(t, f.remote, "rev-parse", "trunk"); landed != f.head {
		t.Fatal("push did not land before release failed")
	}
	ledger, err := readOperatorLedger(filepath.Join(f.root, "operator-actions.json"))
	if err != nil || len(ledger.Actions) != 1 {
		t.Fatalf("checkout action missing after push: %+v, %v", ledger, err)
	}
}

func TestRecoveredLandingRejectsChangedOrReusedResult(t *testing.T) {
	for _, mode := range []string{
		"reused lease", "missing slot", "reused during reservation", "busy after reservation",
		"wrong path response", "missing receipt",
		"old receipt", "held lease", "wrong base",
	} {
		t.Run(mode, func(t *testing.T) {
			f, pool := recoveredLandingFixture(t)
			before := landingGit(t, f.remote, "rev-parse", "trunk")
			switch mode {
			case "reused lease":
				writeRecoveredPoolStatus(
					t,
					os.Getenv("CONCIERGE_TEST_POOL_STATUS"),
					f.worktree,
					"new-lease",
					"another-task",
				)
			case "missing slot":
				if err := os.WriteFile(os.Getenv("CONCIERGE_TEST_POOL_STATUS"), []byte("[]"), 0600); err != nil {
					t.Fatal(err)
				}
			case "reused during reservation":
				t.Setenv("CONCIERGE_TEST_LEASE_FAIL", "1")
			case "busy after reservation":
				t.Setenv("CONCIERGE_TEST_LEASE_BUSY", "1")
			case "wrong path response":
				t.Setenv("CONCIERGE_TEST_WRONG_PATH", filepath.Join(t.TempDir(), "other"))
			case "old receipt":
				changeRecoveredTask(
					t,
					f.root,
					func(snapshot *taskstate.Snapshot) { snapshot.Receipt.Generation = "newer" },
				)
			case "missing receipt":
				changeRecoveredTask(
					t,
					f.root,
					func(snapshot *taskstate.Snapshot) { snapshot.Receipt = nil },
				)
			case "held lease":
				changeRecoveredTask(
					t,
					f.root,
					func(snapshot *taskstate.Snapshot) { snapshot.Receipt.Lease.State = taskstate.LeaseHeld },
				)
			case "wrong base":
				changeRecoveredTask(
					t,
					f.root,
					func(snapshot *taskstate.Snapshot) { snapshot.Receipt.Lease.BaseCommit = strings.Repeat("a", 40) },
				)
			}
			expectLifecycleError(t, f.root, 1, recoveredLandingArgs(f, pool)...)
			if got := landingGit(t, f.remote, "rev-parse", "trunk"); got != before {
				t.Fatal("rejected result changed the remote")
			}
			if mode == "wrong path response" {
				data, err := os.ReadFile(os.Getenv("CONCIERGE_TEST_POOL_STATUS"))
				if err != nil || strings.Contains(string(data), "recovery-lease") {
					t.Fatalf("unconfirmed reservation remains: %s, %v", data, err)
				}
			}
		})
	}
}
