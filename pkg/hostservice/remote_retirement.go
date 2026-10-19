package hostservice

import (
	"context"
	"errors"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

// Retain the completed worker until transport cleanup is confirmed. Its verifier
// and lease are required to repeat an interrupted cleanup without guessing.
func (server *Server) retireRemoteTransport(ctx context.Context, taskID, generation string) error {
	_, err := server.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		snapshot := current.Snapshot()
		if snapshot.Completion == nil || snapshot.Completion.Generation != generation {
			if current.HasReceipt(generation, taskstate.Close) ||
				current.HasReceipt(generation, taskstate.Retain) {
				return nil
			}
			return taskstate.ErrInvalidTransition
		}
		if _, err := current.RetireResult(generation); err != nil {
			return err
		}
		worker := snapshot.Worker
		if worker == nil || worker.Machine == "" {
			return nil
		}
		location := worker.Lease.Remote
		if location == (taskstate.RemoteLease{}) && worker.Access == nil {
			return nil
		}
		return server.revokeRemoteWorkerTransportLocked(ctx, locked, generation)
	})
	return err
}

func (server *Server) revokeRemoteWorkerTransportLocked(
	ctx context.Context,
	locked *taskstate.LockedTask,
	generation string,
) error {
	snapshot := locked.Current().Snapshot()
	taskID := locked.Current().ID()
	worker := snapshot.Worker
	if worker == nil {
		return workeraccess.ErrDenied
	}
	location := worker.Lease.Remote
	grant := worker.Access
	if grant == nil || !grant.Revoked || grant.Validate() != nil ||
		grant.Task != taskID || grant.Generation != generation ||
		worker.Generation != generation ||
		location == (taskstate.RemoteLease{}) || location.Validate() != nil ||
		location.PrimaryRoot != server.config.Root || location.Machine != worker.Machine ||
		location.Repository != snapshot.Definition.Repo || location.ProjectPath() != worker.Project ||
		worker.Lease.Path != worker.Worktree || server.tunnels == nil {
		return workeraccess.ErrDenied
	}
	machines := server.preparationMachines
	if machines == nil {
		machines = herdr.MachineCatalog{
			Runner: herdr.NewClient(server.config.HerdrExecutable).Runner,
		}
	}
	profile, err := machines.Resolve(ctx, worker.Machine)
	if err != nil {
		return err
	}
	if !profile.Enabled || profile.ID != worker.Machine || profile.Target != location.Target ||
		profile.Session != worker.Session {
		return remoteworker.ErrTunnelOwnership
	}
	location.Backend = ""
	request := remoteworker.LaunchSiteRequest{
		Worktree: remoteworker.WorktreeRequest{
			Task: taskID, Location: location, Holder: worker.Lease.Holder,
			Branch: worker.Lease.Branch, BaseCommit: worker.Lease.BaseCommit,
		},
		Lease: worker.Lease, Generation: generation,
	}
	tools, err := request.Tools()
	if err != nil {
		return err
	}
	if err := locked.CheckCurrent(); err != nil {
		return err
	}
	if err := server.tunnels.Revoke(ctx, remoteworker.SocketOwner{
		Task: taskID, Generation: generation, Verifier: grant.Verifier,
	}); err != nil {
		return err
	}
	server.closeTaskWorkerSocket(taskID)
	if err := locked.CheckCurrent(); err != nil {
		return err
	}
	runner := server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	if err := (remoteworker.SecretTransport{Runner: runner}).Remove(ctx, profile, tools, grant.Verifier); err != nil {
		return err
	}
	if err := locked.CheckCurrent(); err != nil {
		return err
	}
	stage, err := remoteworker.OpenPrimaryCredentialStage(ctx, server.config.Root, request)
	if err != nil {
		return err
	}
	return errors.Join(stage.RemoveSecret(grant.Verifier), stage.Close())
}
