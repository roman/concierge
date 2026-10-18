package hostservice

import (
	"context"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type hostRemoteRuntime struct{ server *Server }

func (runtime hostRemoteRuntime) CheckEndpoint(ctx context.Context, bound taskstate.Worker) error {
	_, err := runtime.profile(ctx, bound)
	return err
}

func (runtime hostRemoteRuntime) profile(
	ctx context.Context,
	bound taskstate.Worker,
) (herdr.SavedMachine, error) {
	location := bound.Lease.Remote
	if location == (taskstate.RemoteLease{}) || location.Machine != bound.Machine ||
		bound.Lease.Project != bound.Project || bound.Lease.Path != bound.Worktree {
		return herdr.SavedMachine{}, workeraccess.ErrDenied
	}
	profile, err := (hostRemoteLeases{server: runtime.server}).profile(ctx, location)
	if err != nil {
		return profile, err
	}
	if profile.Session != bound.Session {
		return profile, remoteworker.ErrWorktreeOwnership
	}
	return profile, nil
}

func (runtime hostRemoteRuntime) Prepare(
	ctx context.Context,
	task *taskstate.LockedTask,
	generation string,
) (taskops.RemoteWorkerSite, error) {
	snapshot := task.Current().Snapshot()
	bound := snapshot.Worker
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil ||
		snapshot.Definition.Hold != nil ||
		bound == nil {
		return taskops.RemoteWorkerSite{}, workeraccess.ErrDenied
	}
	if bound.Lease.Remote.Repository != snapshot.Definition.Repo {
		return taskops.RemoteWorkerSite{}, workeraccess.ErrDenied
	}
	if bound.PendingLaunch != "" {
		if bound.PendingLaunch != generation || len(bound.Restarts) == 0 ||
			bound.Restarts[len(bound.Restarts)-1].To != generation {
			return taskops.RemoteWorkerSite{}, workeraccess.ErrDenied
		}
	} else if generation != bound.Generation || bound.TransportGeneration == generation {
		return taskops.RemoteWorkerSite{}, workeraccess.ErrDenied
	}
	profile, err := runtime.profile(ctx, *bound)
	if err != nil {
		return taskops.RemoteWorkerSite{}, err
	}
	if generation != bound.Generation {
		if err := runtime.server.revokeRemoteWorkerTransportLocked(ctx, task, bound.Generation); err != nil {
			return taskops.RemoteWorkerSite{}, err
		}
	}
	location := bound.Lease.Remote
	location.Backend = ""
	request := remoteworker.LaunchSiteRequest{
		Worktree: remoteworker.WorktreeRequest{Task: task.Current().ID(), Location: location,
			Holder: bound.Lease.Holder, Branch: bound.Lease.Branch, BaseCommit: bound.Lease.BaseCommit},
		Lease: bound.Lease, Generation: generation,
	}
	if err := task.CheckCurrent(); err != nil {
		return taskops.RemoteWorkerSite{}, err
	}
	site, err := (hostRemoteLeases{server: runtime.server}).transport().
		PrepareLaunchSite(ctx, profile, request)
	return taskops.RemoteWorkerSite{Generation: generation, Profile: profile, Site: site}, err
}

func (runtime hostRemoteRuntime) Activate(
	ctx context.Context,
	task *taskstate.LockedTask,
	site taskops.RemoteWorkerSite,
) error {
	bound := task.Current().Snapshot().Worker
	if bound == nil {
		return workeraccess.ErrDenied
	}
	if err := runtime.CheckEndpoint(ctx, *bound); err != nil {
		return err
	}
	return runtime.server.activateRemoteWorkerLocked(
		ctx,
		task,
		site.Generation,
		site.Profile,
		site.Site.Tools,
	)
}
