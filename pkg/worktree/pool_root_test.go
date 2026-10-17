package worktree

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestPoolAcquireConstrainsConfiguredWorktreePaths(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprintf("outside=%t", outside), func(t *testing.T) {
			project, pool := t.TempDir(), t.TempDir()
			path := filepath.Join(pool, "slot", "repo")
			if outside {
				path = filepath.Join(t.TempDir(), "foreign")
			}
			lease := Lease{Path: path, ID: "lease-1", Holder: "task-holder"}
			inventory, err := json.Marshal(
				[]map[string]any{
					{
						"path":         path,
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
			acquired, err := json.Marshal(lease)
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(t.TempDir(), "get-calls")
			script := `#!/bin/sh
[ "$1" = --root ] && [ "$2" = ` + sshcommand.Quote(pool) + ` ] || exit 70
shift 2
case "$1" in
status) printf '%s' ` + sshcommand.Quote(string(inventory)) + ` ;;
get)
 printf called >> ` + sshcommand.Quote(record) + `
 [ "$7" = --worktree-path ] && [ "$8" = '{pool}/{slot}/{repo}' ] || exit 71
 printf '%s' ` + sshcommand.Quote(string(acquired)) + ` ;;
*) exit 72 ;;
esac
`
			executable := filepath.Join(t.TempDir(), "treehouse")
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			_, err = (PoolClient{Executable: executable, Root: pool}).Acquire(
				context.Background(),
				project,
				lease.Holder,
			)
			if outside {
				if err == nil {
					t.Fatal("accepted inventory outside the remote base")
				}
				if _, err := os.Stat(record); !os.IsNotExist(err) {
					t.Fatal("acquired before excluding a foreign worktree")
				}
				return
			}
			if err != nil {
				t.Fatalf("did not constrain the new worktree path: %v", err)
			}
		})
	}
}
