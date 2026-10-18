package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportResultPinsUnpublishedCommitWithoutMovingPrimary(t *testing.T) {
	client, spec := checkoutForTest(t)
	source := filepath.Join(t.TempDir(), "remote")
	gitForTest(t, spec.Project, "clone", "--", spec.Project, source)
	gitForTest(
		t,
		source,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Unpublished result",
	)
	commit := gitForTest(t, source, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(spec.Project, "file.txt"), []byte("operator changes"), 0600); err != nil {
		t.Fatal(err)
	}
	fetchReceipt := filepath.Join(spec.Project, ".git", "FETCH_HEAD")
	if err := os.WriteFile(fetchReceipt, []byte("retained fetch receipt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reference := "refs/concierge/results/task-generation"
	ctx := context.Background()
	for range 2 {
		bundle, err := client.ExportCommit(ctx, source, commit)
		if err != nil {
			t.Fatal(err)
		}
		err = client.ImportResult(ctx, spec.Project, commit, reference, bundle)
		bundle.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if head := gitForTest(t, spec.Project, "rev-parse", "HEAD"); head != spec.BaseCommit {
		t.Fatal("result import moved the primary branch")
	}
	if data, err := os.ReadFile(filepath.Join(spec.Project, "file.txt")); err != nil ||
		string(data) != "operator changes" {
		t.Fatal("result import changed operator files")
	}
	if data, err := os.ReadFile(fetchReceipt); err != nil ||
		string(data) != "retained fetch receipt\n" {
		t.Fatal("result import replaced FETCH_HEAD")
	}
	if head := gitForTest(t, spec.Project, "rev-parse", reference); head != commit {
		t.Fatal("result import selected another commit")
	}
	spec.BaseCommit = commit
	if err := client.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if head := gitForTest(t, spec.Path, "rev-parse", "HEAD"); head != commit {
		t.Fatal("materialized another result")
	}
}

func TestImportResultRejectsAnotherBundleBeforeAddingObjects(t *testing.T) {
	for _, mismatch := range []string{"commit", "mutable", "reference", "existing reference", "truncated"} {
		t.Run(mismatch, func(t *testing.T) {
			client, spec := checkoutForTest(t)
			source := filepath.Join(t.TempDir(), "remote")
			gitForTest(t, spec.Project, "clone", "--", spec.Project, source)
			gitForTest(
				t,
				source,
				"-c",
				"user.name=Test",
				"-c",
				"user.email=test@example.invalid",
				"commit",
				"--allow-empty",
				"-m",
				"Unpublished result",
			)
			commit := gitForTest(t, source, "rev-parse", "HEAD")
			bundle, err := client.ExportCommit(context.Background(), source, commit)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			selected := commit
			reference := "refs/concierge/results/task-generation"
			switch mismatch {
			case "commit":
				selected = spec.BaseCommit
			case "mutable":
				selected = "HEAD"
			case "reference":
				reference = "refs/heads/main"
			case "existing reference":
				gitForTest(t, spec.Project, "update-ref", reference, spec.BaseCommit)
			case "truncated":
				if err := client.ImportResult(context.Background(), spec.Project, selected, reference, strings.NewReader("incomplete")); err == nil {
					t.Fatal("accepted an incomplete result")
				}
				return
			}
			if err := client.ImportResult(context.Background(), spec.Project, selected, reference, bundle); err == nil {
				t.Fatal("accepted another result or replaced a foreign ref")
			}
			if exec.Command(client.Executable, "-C", spec.Project, "cat-file", "-e", commit).
				Run() ==
				nil {
				t.Fatal("added objects before checking the immutable result")
			}
		})
	}
}
