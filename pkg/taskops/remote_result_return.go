package taskops

import (
	"context"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func (service *Service) returnPrimaryResultLease(
	ctx context.Context,
	task taskstate.Task,
	generation string,
) (taskstate.Task, error) {
	result, found := task.RetiredRemoteResult(generation)
	if !found || result.Lease.State == taskstate.LeaseReturned {
		return task, nil
	}
	lease := result.Lease
	if lease.State != taskstate.LeaseHeld || lease.Remote != (taskstate.RemoteLease{}) {
		return taskstate.Task{}, taskstate.ErrInvalidTransition
	}
	current, found, err := service.pool.Find(ctx, lease.Project, lease.Holder)
	if err != nil {
		return taskstate.Task{}, err
	}
	if found {
		if current != (worktree.Lease{ID: lease.ID, Holder: lease.Holder, Path: lease.Path}) {
			return taskstate.Task{}, worktree.ErrIdentity
		}
		specification := worktree.Specification{
			Project:    lease.Project,
			Path:       lease.Path,
			Branch:     lease.Branch,
			BaseCommit: result.Commit,
		}
		if err := service.git.PrepareLeased(ctx, specification); err != nil {
			return taskstate.Task{}, err
		}
		if err := service.git.VerifyReturn(ctx, specification); err != nil {
			return taskstate.Task{}, err
		}
	}
	if err := service.pool.Return(ctx, lease.Project, worktree.Lease{ID: lease.ID, Holder: lease.Holder, Path: lease.Path}); err != nil {
		return taskstate.Task{}, err
	}
	return task.RecordPrimaryResultLeaseReturned(generation, lease)
}
