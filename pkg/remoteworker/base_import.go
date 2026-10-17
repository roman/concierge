package remoteworker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ImportBase retains an unpublished base in a task-scoped ref before acquisition.
// The primary paths in the request participate only in the ownership digest.
func (manager Worktrees) ImportBase(
	ctx context.Context,
	request WorktreeRequest,
	input io.Reader,
) error {
	if request.Validate() != nil || input == nil {
		return ErrWorktreeOwnership
	}
	project, err := manager.PrepareProject(ctx, request.Location)
	if err != nil {
		return err
	}
	return withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		if record != nil &&
			(record.Phase == worktreeReturning || record.Phase == worktreeReturned) {
			return ErrWorktreeOwnership
		}
		reference := "refs/concierge/bases/" + worktreeOwnershipKey(request)
		current, err := manager.projectGit(
			ctx,
			project,
			"for-each-ref",
			"--format=%(objectname)",
			reference,
		)
		if err != nil || (current != "" && current != request.BaseCommit) {
			return ErrWorktreeOwnership
		}
		directory, err := os.MkdirTemp(request.Location.Base, ".base-import-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(directory)
		path := filepath.Join(directory, "base.bundle")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(file, input); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		heads, err := manager.projectGit(ctx, project, "bundle", "list-heads", path, "HEAD")
		if err != nil || heads != request.BaseCommit+" HEAD" {
			return ErrWorktreeOwnership
		}
		if _, err := manager.projectGit(ctx, project, "bundle", "verify", path); err != nil {
			return err
		}
		// Avoid the repository-wide FETCH_HEAD while other tasks import their bases.
		if _, err := manager.projectGit(ctx, project, "fetch", "--no-tags", "--no-write-fetch-head", "--", path, "HEAD"); err != nil {
			return err
		}
		if current == "" {
			if _, err := manager.projectGit(ctx, project, "update-ref", reference, request.BaseCommit, strings.Repeat("0", len(request.BaseCommit))); err != nil {
				return err
			}
		}
		captured, err := manager.projectGit(ctx, project, "rev-parse", "--verify", reference)
		if err != nil || captured != request.BaseCommit {
			return ErrWorktreeOwnership
		}
		return nil
	})
}
