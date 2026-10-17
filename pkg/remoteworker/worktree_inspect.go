package remoteworker

import (
	"context"
	"path/filepath"

	"github.com/roman/concierge/pkg/taskstate"
)

// Inspect recovers a completed acquisition without continuing an uncertain get.
func (manager Worktrees) Inspect(
	ctx context.Context,
	request WorktreeRequest,
) (taskstate.WorktreeLease, error) {
	if request.Validate() != nil || !filepath.IsAbs(manager.Git.Executable) {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	var captured taskstate.WorktreeLease
	err := withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil || record == nil || record.Phase != worktreeReady {
			return ErrWorktreeOwnership
		}
		if err := manager.verifyLease(ctx, *record); err != nil {
			return err
		}
		captured = record.Lease
		return nil
	})
	return captured, err
}
