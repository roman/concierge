package remoteworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemoteBaseImportTransfersUnpublishedCommitBeforeAcquisition(t *testing.T) {
	for _, name := range []string{"selected base", "wrong bundle"} {
		t.Run(
			name,
			func(t *testing.T) {
				mismatch := name == "wrong bundle"
				manager, location := remoteProjectFixture(t)
				bare := filepath.Join(location.Base, "repositories", "repo.git")
				source := filepath.Join(t.TempDir(), "primary")
				remoteProjectGit(t, manager.Git.Executable, location.Base, "clone", bare, source)
				original := remoteProjectGit(t, manager.Git.Executable, source, "rev-parse", "HEAD")
				if err := os.WriteFile(filepath.Join(source, "unpublished"), []byte("selected source"), 0600); err != nil {
					t.Fatal(err)
				}
				remoteProjectGit(t, manager.Git.Executable, source, "add", "unpublished")
				remoteProjectGit(
					t,
					manager.Git.Executable,
					source,
					"-c",
					"user.name=Test",
					"-c",
					"user.email=test@example.invalid",
					"commit",
					"-m",
					"Unpublished source",
				)
				commit := remoteProjectGit(t, manager.Git.Executable, source, "rev-parse", "HEAD")
				bundle, err := manager.Git.ExportCommit(context.Background(), source, commit)
				if err != nil {
					t.Fatal(err)
				}
				defer bundle.Close()
				request := WorktreeRequest{
					Task:       "task-one",
					Location:   location,
					Holder:     "primary-task-holder",
					Branch:     "task/result",
					BaseCommit: commit,
				}
				if mismatch {
					request.BaseCommit = original
				}
				err = manager.ImportBase(context.Background(), request, bundle)
				if mismatch {
					if err == nil {
						t.Fatal("accepted another bundle HEAD")
					}
					probe := exec.Command(
						manager.Git.Executable,
						"-C",
						bare,
						"cat-file",
						"-e",
						commit,
					)
					if probe.Run() == nil {
						t.Fatal("imported objects before checking the advertised commit")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				lease, err := manager.Acquire(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if head := remoteProjectGit(t, manager.Git.Executable, lease.Path, "rev-parse", "HEAD"); head != commit {
					t.Fatal("acquired another base")
				}
				data, err := os.ReadFile(filepath.Join(lease.Path, "unpublished"))
				if err != nil || string(data) != "selected source" {
					t.Fatal("lost unpublished source")
				}
			},
		)
	}
}
