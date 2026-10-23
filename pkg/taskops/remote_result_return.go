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
		if err := service.git.VerifyCheckoutCommit(ctx, specification); err != nil {
			return taskstate.Task{}, err
		}
		verify := service.git.VerifyReturn
		if task.HasReceipt(generation, taskstate.Close) {
			// A confirmed native return may predate landing. Check this result
			// independently before discarding the primary checkout's files.
			if task.Definition().Kind != "scout" {
				target := ""
				if service.readSettings != nil {
					settings, err := service.readSettings(task.Definition().Repo)
					if err != nil {
						return taskstate.Task{}, err
					}
					target = settings.Branch
				}
				if err := service.git.VerifyResultLanded(ctx, lease.Project, result.Commit, lease.Branch, target); err != nil {
					return taskstate.Task{}, err
				}
			}
			if err := service.pool.VerifyReturn(ctx, lease.Project, current); err != nil {
				return taskstate.Task{}, err
			}
			verify = service.git.CleanReturn
		}
		if err := verify(ctx, specification); err != nil {
			return taskstate.Task{}, err
		}
	}
	if err := service.pool.Return(ctx, lease.Project, worktree.Lease{ID: lease.ID, Holder: lease.Holder, Path: lease.Path}); err != nil {
		return taskstate.Task{}, err
	}
	return task.RecordPrimaryResultLeaseReturned(generation, lease)
}
