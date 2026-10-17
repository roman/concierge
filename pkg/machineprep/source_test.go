package machineprep

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceBundleRetainsCommittedSourceWithoutPush(t *testing.T) {
	source := t.TempDir()
	gitCommand(t, source, "init", "-b", "main")
	gitCommand(t, source, "remote", "add", "origin", "ssh://git.test/concierge.git")
	commitFixture(t, source, "committed client")
	commit := gitCommand(t, source, "rev-parse", "HEAD")
	bundle, err := BundleSource(context.Background(), source, "ssh://git.test/concierge.git")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	if bundle.Commit != commit {
		t.Fatal("source bundle has another commit")
	}
	data, err := os.ReadFile(bundle.file.Name())
	if err != nil || len(data) == 0 {
		t.Fatal("source bundle is absent")
	}
	if info, err := os.Stat(bundle.file.Name()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("source bundle is not private")
	}
	receiver := t.TempDir()
	gitCommand(t, receiver, "init", "--bare")
	gitCommand(t, receiver, "fetch", bundle.file.Name(), "HEAD")
	if got := gitCommand(t, receiver, "rev-parse", "FETCH_HEAD"); got != commit {
		t.Fatal("bundle changed source identity")
	}
	directory := bundle.directory
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("closed source bundle remains")
	}
}

func TestSourceBundleRejectsOtherOriginsAndUncommittedFiles(t *testing.T) {
	for _, kind := range []string{"other origin", "tracked edit", "untracked file"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			gitCommand(t, source, "init", "-b", "main")
			gitCommand(t, source, "remote", "add", "origin", "ssh://git.test/concierge.git")
			commitFixture(t, source, "committed client")
			url := "ssh://git.test/concierge.git"
			if kind == "other origin" {
				url = "ssh://other.test/project.git"
			}
			if kind == "tracked edit" {
				if err := os.WriteFile(filepath.Join(source, "README"), []byte("keep edits"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "untracked file" {
				if err := os.WriteFile(filepath.Join(source, "keep"), []byte("keep edits"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			bundle, err := BundleSource(context.Background(), source, url)
			if bundle != nil {
				_ = bundle.Close()
			}
			if err == nil {
				t.Fatal("bundled an unapproved or dirty source")
			}
		})
	}
}
