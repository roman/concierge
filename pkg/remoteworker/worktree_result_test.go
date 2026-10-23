package remoteworker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestRemoteResultExportsIgnoredOutputsWithoutDeletingThem(t *testing.T) {
	manager, held := remoteReturnFixture(t)
	ignored := []string{".claude", ".devenv", ".mcp.json", ".pre-commit-config.yaml"}
	if err := os.WriteFile(filepath.Join(held.Lease.Path, ".gitignore"), []byte(strings.Join(ignored, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "add", ".gitignore")
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
		"Ignore generated outputs",
	)
	commit := remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "rev-parse", "HEAD")
	outputs := []string{
		".claude/settings.json",
		".devenv/state",
		".mcp.json",
		".pre-commit-config.yaml",
	}
	for _, name := range outputs {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(held.Lease.Path, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(held.Lease.Path, name), []byte("generated output"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := manager.ExportResult(
		t.Context(),
		WorktreeResultRequest{Worktree: held.Worktree, Lease: held.Lease, Commit: commit},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Commit() != commit {
		t.Fatal("export selected another commit")
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	held.Proof = WorktreeReturnProof{Outcome: WorktreeAbandoned, Commit: commit}
	if _, err := manager.Return(t.Context(), held); err == nil {
		t.Fatal("returned a checkout with ignored outputs")
	}
	for _, name := range outputs {
		if data, err := os.ReadFile(filepath.Join(held.Lease.Path, name)); err != nil ||
			string(data) != "generated output" {
			t.Fatalf("changed ignored output %s: %v", name, err)
		}
	}
	if lease, err := manager.Inspect(t.Context(), held.Worktree); err != nil ||
		lease != held.Lease {
		t.Fatalf("export or failed return changed lease: %+v %v", lease, err)
	}
}

func TestRemoteResultBundleFailureRetainsLeaseAndReportsStage(t *testing.T) {
	manager, held := remoteReturnFixture(t)
	executable := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor argument do\nif [ \"$argument\" = clone ]; then exit 1; fi\ndone\nexec " + sshcommand.Quote(
		manager.Git.Executable,
	) + " \"$@\"\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager.Git.Executable = executable
	bundle, err := manager.ExportResult(
		t.Context(),
		WorktreeResultRequest{
			Worktree: held.Worktree,
			Lease:    held.Lease,
			Commit:   held.Worktree.BaseCommit,
		},
	)
	if bundle != nil || !errors.Is(err, ErrResultExport) ||
		!strings.Contains(err.Error(), "cannot create bundle") {
		t.Fatalf("bundle failure lost its diagnosis: bundle=%v err=%v", bundle, err)
	}
	if lease, err := manager.Inspect(t.Context(), held.Worktree); err != nil ||
		lease != held.Lease {
		t.Fatalf("bundle failure changed the lease: %+v %v", lease, err)
	}
}

func TestRemoteResultRejectsUnfinishedContentsWithPaths(t *testing.T) {
	for _, kind := range []string{"untracked", "tracked", "staged"} {
		t.Run(kind, func(t *testing.T) {
			manager, held := remoteReturnFixture(t)
			name := "unfinished file.txt"
			if kind != "untracked" {
				name = "tracked.txt"
				if err := os.WriteFile(filepath.Join(held.Lease.Path, name), []byte("committed"), 0600); err != nil {
					t.Fatal(err)
				}
				remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "add", name)
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
					"Track contents",
				)
			}
			if err := os.WriteFile(filepath.Join(held.Lease.Path, name), []byte("unfinished"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "staged" {
				remoteProjectGit(t, manager.Git.Executable, held.Lease.Path, "add", name)
			}
			bundle, err := manager.ExportResult(
				t.Context(),
				WorktreeResultRequest{Worktree: held.Worktree, Lease: held.Lease},
			)
			if bundle != nil || !errors.Is(err, ErrResultDirty) ||
				!strings.Contains(err.Error(), name) ||
				errors.Is(err, ErrWorktreeOwnership) {
				t.Fatalf(
					"unfinished contents must name the path separately from ownership: bundle=%v err=%v",
					bundle,
					err,
				)
			}
		})
	}
}

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
