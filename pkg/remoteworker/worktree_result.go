package remoteworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

var ErrResultDirty = errors.New(
	"remote result has unfinished files. Retain the lease and commit or preserve these changes before retrying export",
)

var ErrResultExport = errors.New(
	"remote result export failed. Retain the lease and selected commit",
)

type WorktreeResultRequest struct {
	Worktree WorktreeRequest         `json:"worktree"`
	Lease    taskstate.WorktreeLease `json:"lease"`
	Commit   string                  `json:"commit,omitempty"`
}

type WorktreeResult struct {
	Lease  taskstate.WorktreeLease `json:"lease"`
	Commit string                  `json:"commit"`
}

func (request WorktreeResultRequest) Validate() error {
	record := worktreeRecord{Format: 1, Request: request.Worktree,
		Backend: request.Lease.Remote.Backend, Phase: worktreeReady, Lease: request.Lease}
	if record.validate() != nil {
		return ErrWorktreeOwnership
	}
	if request.Commit != "" {
		return (WorktreeReturnProof{Outcome: WorktreeLanded, Commit: request.Commit}).validate(
			request.Worktree,
		)
	}
	return nil
}

func DecodeWorktreeResultRequest(input io.Reader) (WorktreeResultRequest, error) {
	var request WorktreeResultRequest
	if decodeSetupRequest(input, &request) != nil || request.Validate() != nil {
		return WorktreeResultRequest{}, ErrWorktreeOwnership
	}
	return request, nil
}

// ExportResult snapshots committed work while retaining the original lease.
// Ignored outputs stay in the checkout for inspection and lease-return cleanup.
// A retry with a selected commit refuses a subsequent remote branch update.
func (manager Worktrees) ExportResult(
	ctx context.Context,
	request WorktreeResultRequest,
) (*worktree.CommitBundle, error) {
	if request.Validate() != nil || !filepath.IsAbs(manager.Git.Executable) {
		return nil, ErrWorktreeOwnership
	}
	var bundle *worktree.CommitBundle
	err := withWorktreeJournal(ctx, request.Worktree, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil || record == nil || record.Phase != worktreeReady ||
			record.Lease != request.Lease {
			return ErrWorktreeOwnership
		}
		if err := manager.verifyLease(ctx, *record); err != nil {
			return err
		}
		status, err := manager.projectGit(
			ctx,
			record.Lease.Path,
			"status",
			"--porcelain=v1",
			"--untracked-files=all",
			"--ignored=no",
			"--ignore-submodules=none",
		)
		if err != nil {
			return fmt.Errorf("%w: cannot inspect checkout status: %v", ErrResultExport, err)
		}
		if status != "" {
			return fmt.Errorf("%w: %s", ErrResultDirty, status)
		}
		commit, err := manager.projectGit(ctx, record.Lease.Path, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("%w: cannot resolve checkout HEAD: %v", ErrResultExport, err)
		}
		if request.Commit != "" && request.Commit != commit {
			return fmt.Errorf(
				"%w: checkout HEAD %s differs from selected commit %s",
				ErrWorktreeOwnership,
				commit,
				request.Commit,
			)
		}
		bundle, err = manager.Git.ExportCommit(ctx, record.Lease.Path, commit)
		if err != nil {
			return fmt.Errorf("%w: cannot create bundle for %s: %v", ErrResultExport, commit, err)
		}
		return nil
	})
	if err != nil && bundle != nil {
		bundle.Close()
		bundle = nil
	}
	return bundle, err
}
