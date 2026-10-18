package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLandingEvidenceRequiresTheCommitOnTheFetchedDefaultBranch(t *testing.T) {
	client, spec := checkoutForTest(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitForTest(t, spec.Project, "clone", "--bare", spec.Project, origin)
	gitForTest(t, spec.Project, "remote", "add", "origin", origin)
	if err := client.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	gitForTest(
		t,
		spec.Path,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Result",
	)
	commit := gitForTest(t, spec.Path, "rev-parse", "HEAD")
	ctx := context.Background()
	branch, err := client.FetchDefaultBranch(ctx, spec.Project)
	if err != nil || branch.Name != "main" || branch.Tip != spec.BaseCommit {
		t.Fatalf("default branch: %+v %v", branch, err)
	}
	if err := client.VerifyLanded(ctx, spec.Project, commit, branch); !errors.Is(
		err,
		ErrResultNotLanded,
	) {
		t.Fatalf("unlanded result: %v", err)
	}
	if err := client.VerifyLanded(ctx, spec.Project, spec.BaseCommit, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Project, "file.txt"), []byte("operator edit"), 0600); err != nil {
		t.Fatal(err)
	}
	fetchReceipt := filepath.Join(spec.Project, ".git", "FETCH_HEAD")
	if err := os.WriteFile(fetchReceipt, []byte("retain fetch receipt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForTest(t, spec.Path, "push", "origin", commit+":refs/heads/main")
	branch, err = client.FetchDefaultBranch(ctx, spec.Project)
	if err != nil || branch.Tip != commit {
		t.Fatalf("fresh default tip: %+v %v", branch, err)
	}
	if err := client.VerifyLanded(ctx, spec.Project, commit, branch); err != nil {
		t.Fatal(err)
	}
	if head := gitForTest(t, spec.Project, "rev-parse", "HEAD"); head != spec.BaseCommit {
		t.Fatal("landing inspection moved the operator checkout")
	}
	if data, err := os.ReadFile(filepath.Join(spec.Project, "file.txt")); err != nil ||
		string(data) != "operator edit" {
		t.Fatal("landing inspection changed operator edits")
	}
	if data, err := os.ReadFile(fetchReceipt); err != nil ||
		string(data) != "retain fetch receipt\n" {
		t.Fatal("landing inspection replaced FETCH_HEAD")
	}
	if err := client.VerifyLanded(ctx, spec.Project, "HEAD", branch); err == nil {
		t.Fatal("accepted a moving result name")
	}
}
