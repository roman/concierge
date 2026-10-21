package conciergecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestBacklogRecoverOrphanedTask(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	root := lifecycleRoot(t)
	claimNativeParent(t, root)
	store, err := taskstate.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(
		context.Background(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			snapshot.State = taskstate.InFlight
			snapshot.Worker.PendingLaunch = "gen-2"
			snapshot.Worker.Project = root
			snapshot.Worker.Lease = taskstate.WorktreeLease{
				ID:         "lease",
				Holder:     "holder",
				Project:    root,
				Path:       "/worktree",
				Branch:     "task/work",
				BaseCommit: strings.Repeat("a", 40),
				State:      taskstate.LeaseHeld,
			}
			return taskstate.NewMigrated(snapshot)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	herdr := filepath.Join(root, "herdr")
	script := `#!/bin/sh
case "$1 $2" in
'pane get') printf '%s\n' '{"error":{"code":"pane_not_found"}}'; exit 1 ;;
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`
	if err := os.WriteFile(herdr, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(root, "treehouse")
	if err := os.WriteFile(pool, []byte("#!/bin/sh\n[ \"$1\" = status ] || exit 72\nprintf '%s\\n' '[{\"path\":\"/worktree\",\"lease_id\":\"lease\",\"lease_holder\":\"holder\",\"flavor\":\"git\",\"processes\":[]}]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	serveConfiguredTaskRoot(
		t,
		hostservice.Config{Root: root, MessageRoot: filepath.Join(root, "messages"),
			Recipient: recipient, HerdrExecutable: herdr, TreehouseExecutable: pool, Recovery: hostservice.RecoveryMaintenance},
	)
	args := []string{
		"backlog",
		"recover",
		"task-1",
		"--generation",
		"gen-1",
		"--reason",
		"Superseded",
		"--closed-on",
		"2026-10-20",
	}
	if got := expectLifecycleError(t, root, 2, args...); !strings.Contains(
		got,
		"operator-approved",
	) {
		t.Fatal(got)
	}
	args = append(args, "--operator-approved")
	for range 2 {
		got := invoke(t, root, "", args...)
		if !strings.Contains(got, "state: done") || !strings.Contains(got, "lease: held") ||
			!strings.Contains(got, `reason: "Superseded"`) {
			t.Fatal(got)
		}
	}
}
