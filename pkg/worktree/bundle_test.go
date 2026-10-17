package worktree

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportCommitPreservesDirtyPrimaryAndSelectsImmutableBase(t *testing.T) {
	client, specification := checkoutForTest(t)
	gitForTest(
		t,
		specification.Project,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Later",
	)
	if err := os.WriteFile(filepath.Join(specification.Project, "file.txt"), []byte("operator edits\n"), 0600); err != nil {
		t.Fatal(err)
	}
	head := gitForTest(t, specification.Project, "rev-parse", "HEAD")
	refs := gitForTest(t, specification.Project, "show-ref")
	status := gitForTest(t, specification.Project, "status", "--porcelain")
	bundle, err := client.ExportCommit(
		context.Background(),
		specification.Project,
		specification.BaseCommit,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if bundle.Commit() != specification.BaseCommit {
		t.Fatal("exported the moving HEAD")
	}
	if info, err := bundle.file.Stat(); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("bundle is not private")
	}
	if info, err := os.Stat(bundle.directory); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("bundle directory is not private")
	}
	destination := t.TempDir()
	gitForTest(t, destination, "init", "--bare")
	path := filepath.Join(destination, "source.bundle")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(file, bundle)
	if err := file.Close(); copyErr != nil || err != nil {
		t.Fatalf("bundle transfer: %v %v", copyErr, err)
	}
	gitForTest(t, destination, "fetch", "--no-tags", "--", path, "HEAD")
	if got := gitForTest(t, destination, "rev-parse", "FETCH_HEAD"); got != specification.BaseCommit {
		t.Fatal("imported another commit")
	}
	if got := gitForTest(t, specification.Project, "rev-parse", "HEAD"); got != head {
		t.Fatal("moved primary HEAD")
	}
	if got := gitForTest(t, specification.Project, "show-ref"); got != refs {
		t.Fatal("changed primary refs")
	}
	if got := gitForTest(t, specification.Project, "status", "--porcelain"); got != status {
		t.Fatal("changed primary files")
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bundle.directory); !os.IsNotExist(err) {
		t.Fatal("retained temporary bundle files")
	}
}

func TestExportCommitRejectsMovingOrMissingBase(t *testing.T) {
	client, specification := checkoutForTest(t)
	for _, base := range []string{"HEAD", strings.Repeat("a", 40)} {
		if bundle, err := client.ExportCommit(context.Background(), specification.Project, base); err == nil {
			bundle.Close()
			t.Fatal("exported an unproven immutable base")
		}
	}
}
