package taskops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type RemoteWorktrees interface {
	ImportBase(context.Context, herdr.SavedMachine, remoteworker.WorktreeRequest, io.Reader) error
	Acquire(
		context.Context,
		herdr.SavedMachine,
		remoteworker.WorktreeRequest,
	) (taskstate.WorktreeLease, error)
	PrepareLaunchSite(
		context.Context,
		herdr.SavedMachine,
		remoteworker.LaunchSiteRequest,
	) (remoteworker.LaunchSite, error)
}

type RemoteInitialLaunchConfig struct {
	Profile   herdr.SavedMachine
	Worktrees RemoteWorktrees
	Activate  func(context.Context, string, string, RemoteWorkerTools) error
}

func (service *Service) validateRemoteInitialLaunch(
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) error {
	remote := config.Remote
	if remote == nil || remote.Worktrees == nil || !remote.Profile.Enabled ||
		intent.Machine == "" ||
		intent.Remote == (taskstate.RemoteLease{}) ||
		intent.Remote.Validate() != nil ||
		intent.Remote.Backend != "" {
		return ErrRemoteLaunchUnavailable
	}
	location := intent.Remote
	if location.PrimaryRoot != service.root || location.ProjectPath() != intent.Project ||
		location.Machine != intent.Machine ||
		remote.Profile.ID != intent.Machine ||
		remote.Profile.Target != location.Target ||
		remote.Profile.Session != intent.Session {
		return ErrInitialLaunchIdentity
	}
	return nil
}

func remoteInitialWorktreeRequest(
	taskID string,
	intent *taskstate.LaunchIntent,
) remoteworker.WorktreeRequest {
	return remoteworker.WorktreeRequest{
		Task:       taskID,
		Location:   intent.Remote,
		Holder:     intent.LeaseHolder,
		Branch:     intent.Branch,
		BaseCommit: intent.BaseCommit,
	}
}

// prepareRemoteInitialLease captures the native helper's durable acquisition.
// Repeated calls use the holder already fixed in the primary launch intent.
func (service *Service) prepareRemoteInitialLease(
	ctx context.Context,
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) (*taskstate.LaunchIntent, error) {
	if err := service.validateRemoteInitialLaunch(intent, config); err != nil {
		return nil, err
	}
	if intent.Phase == taskstate.LaunchPreparing && intent.Worktree == "" {
		digest := sha256.Sum256([]byte(service.root + "\x00" + taskID + "\x00" + intent.Generation))
		holder := "concierge:" + hex.EncodeToString(digest[:])
		if _, err := service.store.RequestLaunchLease(ctx, taskID, intent.Generation, holder); err != nil {
			return nil, err
		}
		var err error
		_, intent, err = service.initialLaunch(ctx, taskID, intent.Generation)
		if err != nil {
			return nil, err
		}
	}
	if intent.Phase != taskstate.LaunchLeaseRequested {
		return intent, nil
	}
	if err := service.checkInitialLaunchActive(ctx, taskID, intent.Generation); err != nil {
		return nil, err
	}
	request := remoteInitialWorktreeRequest(taskID, intent)
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := config.Git.Validate(ctx, worktree.Specification{Project: intent.Remote.PrimaryProject, Path: intent.Remote.PrimaryProject, Branch: intent.Branch, BaseCommit: intent.BaseCommit}); err != nil {
		return nil, err
	}
	bundle, err := config.Git.ExportCommit(ctx, intent.Remote.PrimaryProject, intent.BaseCommit)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	remote := config.Remote
	if err := remote.Worktrees.ImportBase(ctx, remote.Profile, request, bundle); err != nil {
		return nil, err
	}
	lease, err := remote.Worktrees.Acquire(ctx, remote.Profile, request)
	if err != nil {
		return nil, err
	}
	if _, err := service.store.RecordLaunchLease(ctx, taskID, intent.Generation, lease); err != nil {
		return nil, err
	}
	_, intent, err = service.initialLaunch(ctx, taskID, intent.Generation)
	return intent, err
}
