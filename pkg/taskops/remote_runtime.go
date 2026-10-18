package taskops

import (
	"context"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

type RemoteWorkerSite struct {
	Generation string
	Profile    herdr.SavedMachine
	Site       remoteworker.LaunchSite
}

// RemoteWorkerRuntime performs native setup while the caller holds task authority.
type RemoteWorkerRuntime interface {
	CheckEndpoint(context.Context, taskstate.Worker) error
	Prepare(context.Context, *taskstate.LockedTask, string) (RemoteWorkerSite, error)
	Activate(context.Context, *taskstate.LockedTask, RemoteWorkerSite) error
}

func (worker *SupervisedWorker) WithRemoteRuntime(runtime RemoteWorkerRuntime) *SupervisedWorker {
	configured := *worker
	configured.remoteRuntime = runtime
	return &configured
}

func (worker *SupervisedWorker) prepareRemoteRuntime(
	ctx context.Context,
	task *taskstate.LockedTask,
	generation string,
) (RemoteWorkerSite, error) {
	site, err := worker.remoteRuntime.Prepare(ctx, task, generation)
	if err != nil {
		return RemoteWorkerSite{}, err
	}
	bound := task.Current().Snapshot().Worker
	if bound == nil {
		return RemoteWorkerSite{}, taskstate.ErrInvalidTransition
	}
	location := bound.Lease.Remote
	location.Backend = ""
	request := remoteworker.LaunchSiteRequest{
		Worktree: remoteworker.WorktreeRequest{Task: worker.taskID, Location: location,
			Holder: bound.Lease.Holder, Branch: bound.Lease.Branch, BaseCommit: bound.Lease.BaseCommit},
		Lease: bound.Lease, Generation: generation,
	}
	if site.Generation != generation || !site.Profile.Enabled || site.Profile.ID != bound.Machine {
		return RemoteWorkerSite{}, ErrInitialLaunchIdentity
	}
	if site.Profile.Target != location.Target || site.Profile.Session != bound.Session {
		return RemoteWorkerSite{}, ErrInitialLaunchIdentity
	}
	if site.Site.Validate(request) != nil {
		return RemoteWorkerSite{}, ErrInitialLaunchIdentity
	}
	return site, nil
}

func (worker *SupervisedWorker) activateRemoteRuntime(
	ctx context.Context,
	task *taskstate.LockedTask,
	site RemoteWorkerSite,
) error {
	if err := worker.remoteRuntime.Activate(ctx, task, site); err != nil {
		return err
	}
	bound := task.Current().Snapshot().Worker
	if bound == nil || bound.Generation != site.Generation {
		return ErrRemoteLaunchUnavailable
	}
	if bound.TransportGeneration != site.Generation || bound.Access == nil || bound.Access.Revoked {
		return ErrRemoteLaunchUnavailable
	}
	if bound.Access.Generation != site.Generation || bound.Access.Task != worker.taskID {
		return ErrRemoteLaunchUnavailable
	}
	return nil
}
