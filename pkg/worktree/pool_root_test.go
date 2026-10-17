package worktree

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestPoolClientKeepsRemoteLeasesUnderExplicitRoot(t *testing.T) {
	project := t.TempDir()
	poolRoot := filepath.Join(t.TempDir(), "remote pool")
	lease := Lease{
		Path:   filepath.Join(poolRoot, "slot", "project"),
		ID:     "owned-lease",
		Holder: "task-generation",
	}
	data, err := json.Marshal(
		[]map[string]any{
			{
				"path":         lease.Path,
				"lease_id":     lease.ID,
				"lease_holder": lease.Holder,
				"flavor":       "git",
				"processes":    []any{},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "treehouse")
	script := "#!/bin/sh\n[ \"$1\" = --root ] && [ \"$2\" = " + sshcommand.Quote(
		poolRoot,
	) + " ] || exit 73\nprintf '%s' " + sshcommand.Quote(
		string(data),
	) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TREEHOUSE_ROOT", "/operator/default/pool")
	client := PoolClient{Executable: executable, Root: poolRoot}
	found, exists, err := client.Find(context.Background(), project, lease.Holder)
	if err != nil || !exists || found != lease {
		t.Fatalf("explicit root lost: %+v %v %v", found, exists, err)
	}
	client.Root = "relative/pool"
	if _, _, err := client.Find(context.Background(), project, lease.Holder); err == nil {
		t.Fatal("accepted a relative remote pool root")
	}
}
