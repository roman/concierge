package hostservice

import (
	"context"
	"errors"
	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/presentation"
	"github.com/roman/concierge/pkg/taskstate"
)

const AbandonLaunch Operation = "abandon_launch"

type AbandonedLaunch = taskstate.LaunchAbandonment

func (server *Server) abandonLaunch(
	ctx context.Context,
	input TransitionRequest,
) (Response, error) {
	task, err := server.store.Read(ctx, input.TaskID)
	if err != nil {
		return Response{}, err
	}
	key := controllerKey{kind: taskController, id: input.TaskID}
	release, err := server.controllers.reserveInitialLaunch(key)
	if err != nil {
		return Response{}, err
	}
	defer release()
	if receipt, ok := task.LaunchAbandonment(input.Generation); ok {
		if receipt.Kind == taskstate.DiscardedWorker {
			return Response{}, taskstate.ErrInvalidTransition
		}
		return server.finishAbandonedLaunch(ctx, task, input.Generation)
	}
	clearedIntent := false
	task, err = server.store.WithTask(ctx, input.TaskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if err := current.CheckAbandonInitialLaunch(input.Generation); err != nil {
			return err
		}
		intent := current.Snapshot().LaunchIntent
		if _, ok := current.LaunchAbandonment(input.Generation); ok {
			return nil
		}
		client, err := herdr.NewClient(server.config.HerdrExecutable).
			ForEndpoint(intent.Machine, intent.Session)
		if err != nil {
			return err
		}
		if worker := current.Snapshot().Worker; worker != nil {
			if worker.Machine != "" {
				if _, err := locked.Apply(func(task taskstate.Task) (taskstate.Task, error) { return task.RevokeWorkerAccess(input.Generation) }); err != nil {
					return err
				}
				if err := server.revokeRemoteWorkerTransportLocked(ctx, locked, input.Generation); err != nil {
					return err
				}
			}
			target, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
			if err != nil {
				return err
			}
			if err := presentation.CloseBoundPane(ctx, client, target, worker.Workspace, worker.Tab, locked.CheckCurrent); err != nil {
				return err
			}
		}
		resources := herdr.LaunchResources{Name: intent.Name, Label: intent.Label}
		if intent.Endpoint != nil {
			resources.Tab, resources.Pane = intent.Endpoint.Tab, intent.Endpoint.Pane
		}
		if err := client.CheckLaunchAbsent(ctx, resources); err != nil {
			return err
		}
		if _, err := locked.Apply(func(task taskstate.Task) (taskstate.Task, error) { return task.AbandonInitialLaunch(input.Generation) }); err != nil {
			return err
		}
		_, ok := locked.Current().LaunchAbandonment(input.Generation)
		if !ok {
			return taskstate.ErrInvalidTransition
		}
		clearedIntent = true
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if clearedIntent {
		server.controllers.forgetInitialWakeup(key)
	}
	return server.finishAbandonedLaunch(ctx, task, input.Generation)
}

// A durable bound-worker receipt makes lease return retryable after a lost
// confirmation. Unbound abandonment retains its existing explicit-return policy.
func (server *Server) finishAbandonedLaunch(
	ctx context.Context,
	task taskstate.Task,
	generation string,
) (Response, error) {
	result, ok := task.LaunchAbandonment(generation)
	if !ok {
		return Response{}, taskstate.ErrInvalidTransition
	}
	if result.WorkerRetired && result.Lease != (taskstate.WorktreeLease{}) {
		var err error
		task, err = server.service.ReleaseWorktree(ctx, task.ID(), generation)
		if err != nil {
			return Response{}, err
		}
		result, _ = task.LaunchAbandonment(generation)
	}
	snapshot := task.Snapshot()
	return Response{Transition: &snapshot, Abandoned: &result}, nil
}

func validateAbandonedLaunchResponse(response Response, request Request) error {
	if response.Transition == nil || response.Abandoned == nil ||
		response.Abandoned.Generation != request.Transition.Generation ||
		response.Status != nil ||
		response.Task != nil ||
		response.Assignment != nil ||
		response.Stopped != nil ||
		response.Resolution != nil ||
		response.Control != nil ||
		response.Supervision != nil ||
		response.Error != "" {
		return errors.New("host returned an incomplete abandonment result")
	}
	task, err := taskstate.NewMigrated(*response.Transition)
	if err != nil {
		return err
	}

	receipt, ok := task.LaunchAbandonment(request.Transition.Generation)
	if task.ID() != request.Transition.TaskID || !ok || receipt.Kind != "" ||
		receipt != *response.Abandoned {
		return errors.New("host returned an unapplied abandonment")
	}
	return nil
}
