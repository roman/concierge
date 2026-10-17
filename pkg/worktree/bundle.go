package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CommitBundle struct {
	commit    string
	file      *os.File
	directory string
}

func (bundle *CommitBundle) Commit() string { return bundle.commit }

func (bundle *CommitBundle) Read(data []byte) (int, error) {
	if bundle.file == nil {
		return 0, os.ErrClosed
	}
	return bundle.file.Read(data)
}

func (bundle *CommitBundle) Close() error {
	var err error
	if bundle.file != nil {
		err = bundle.file.Close()
		bundle.file = nil
	}
	return errors.Join(err, os.RemoveAll(bundle.directory))
}

// ExportCommit transfers only the selected commit and its history. The private
// snapshot supplies a named HEAD without changing refs or files in the primary.
func (client Client) ExportCommit(
	ctx context.Context,
	project, commit string,
) (*CommitBundle, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := client.Validate(ctx, Specification{Project: project, Path: filepath.Join(project, "concierge-export"), Branch: "concierge/export", BaseCommit: commit}); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "concierge-commit-bundle-")
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(directory)
		}
	}()
	snapshot := filepath.Join(directory, "snapshot.git")
	if _, err := client.git(ctx, project, "clone", "--shared", "--bare", "--no-hardlinks", "--", project, snapshot); err != nil {
		return nil, err
	}
	if _, err := client.git(ctx, snapshot, "update-ref", "--no-deref", "HEAD", commit); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "commit.bundle")
	if _, err := client.git(ctx, snapshot, "bundle", "create", path, "HEAD"); err != nil {
		return nil, err
	}
	heads, err := client.git(ctx, snapshot, "bundle", "list-heads", path, "HEAD")
	if err != nil || strings.TrimSpace(heads) != commit+" HEAD" {
		return nil, ErrIdentity
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	complete = true
	return &CommitBundle{commit: commit, file: file, directory: directory}, nil
}
