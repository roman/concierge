package remoteworker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteResultExportsOwnedCleanImmutableHeadWithoutReturningLease(t *testing.T) {
	manager, held := remoteReturnFixture(t)
	ctx := context.Background()
	path := filepath.Join(held.Lease.Path, "remote-change.txt")
	if err := os.WriteFile(path, []byte("ship this change"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "add", "remote-change.txt")
	remoteProjectGit(
		t,
		manager.Git.Executable,
		held.Lease.Path,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"-m",
		"Result",
	)
	commit := remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "rev-parse", "HEAD")
	request := WorktreeResultRequest{Worktree: held.Worktree, Lease: held.Lease}
	manager.CheckQuiet = func(context.Context, string) error { t.Fatal("export returned the lease"); return nil }
	for _, selected := range []string{"", commit} {
		request.Commit = selected
		bundle, err := manager.ExportResult(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Commit() != commit {
			t.Fatal("export selected another commit")
		}
		file, err := os.CreateTemp(t.TempDir(), "result-*.bundle")
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(file, bundle)
		closeErr := file.Close()
		bundle.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy result: %v %v", copyErr, closeErr)
		}
		if heads := remoteProjectGit(t, manager.Git.Executable, held.Lease.Project, "bundle", "list-heads", file.Name()); heads != commit+" HEAD" {
			t.Fatalf("exported mutable refs: %s", heads)
		}
	}
	if lease, err := manager.Inspect(ctx, held.Worktree); err != nil || lease != held.Lease {
		t.Fatalf("export changed lease ownership: %+v %v", lease, err)
	}
	for _, mismatch := range []string{"holder", "lease", "commit", "mutable", "dirty"} {
		t.Run(mismatch, func(t *testing.T) {
			changed := request
			switch mismatch {
			case "holder":
				changed.Worktree.Holder = "another-holder"
			case "lease":
				changed.Lease.ID = "another-lease"
			case "commit":
				changed.Commit = held.Worktree.BaseCommit
			case "mutable":
				changed.Commit = "HEAD"
			case "dirty":
				if err := os.WriteFile(path, []byte("unfinished"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if bundle, err := manager.ExportResult(ctx, changed); err == nil {
				bundle.Close()
				t.Fatal("export accepted changed ownership or unfinished work")
			}
		})
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "unfinished") {
		t.Fatal("export changed unfinished work")
	}
}
