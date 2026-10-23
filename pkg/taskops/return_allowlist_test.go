package taskops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestCleanupRetriesWithCurrentReturnIgnoredPaths(t *testing.T) {
	service, _, config, _ := pooledLaunchFixture(t)
	ctx := context.Background()
	launched, err := service.ContinueInitialLaunch(ctx, "task-1", "gen-1", config)
	if err != nil {
		t.Fatal(err)
	}
	bound := launched.Snapshot().Worker
	var approvedPaths []string
	service, err = NewWithWorkerTools(service.root, service.primaryRecipient, WorkerTools{
		Commands: service.workerCommands, Pool: config.Pool, Git: config.Git,
		ReadSettings: func(repo string) (WorkerSettings, error) {
			if repo != launched.Definition().Repo {
				t.Fatalf("read settings for %q", repo)
			}
			return WorkerSettings{ReturnIgnoredPaths: approvedPaths}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bound.Project, ".git", "info", "exclude"), []byte("generated.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(bound.Worktree, "generated.txt")
	if err := os.WriteFile(generated, []byte("regenerable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlaceHold(ctx, "task-1", taskstate.Hold{Reason: "Retain for follow-up"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Retain, ""); err != nil {
		t.Fatal(err)
	}
	runner := &retirementRunner{pane: bound.Pane, response: `{"error":{"code":"pane_not_found"}}`}
	client := herdr.Client{SessionRunner: runner}
	if _, err := service.Retire(ctx, "task-1", "gen-1", client); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReleaseWorktree(ctx, "task-1", "gen-1"); err == nil ||
		!strings.Contains(err.Error(), "generated.txt") {
		t.Fatalf("first cleanup: %v", err)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("blocked cleanup removed file: %v", err)
	}
	approvedPaths = []string{"generated.txt"}
	retired, err := service.ReleaseWorktree(ctx, "task-1", "gen-1")
	if err != nil || retired.Snapshot().Receipt.Lease.State != taskstate.LeaseReturned {
		t.Fatalf("retry: %+v %v", retired.Snapshot().Receipt, err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("file remains: %v", err)
	}
}
