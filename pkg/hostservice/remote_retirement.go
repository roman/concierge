package hostservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	lease := worker.Lease
	partial := snapshot.LaunchIntent != nil
	if partial {
		if err := locked.Current().CheckAbandonInitialLaunch(generation); err != nil {
			return err
		}
		lease = snapshot.LaunchIntent.Lease
	}
	location := lease.Remote
	grant := worker.Access
	if worker.Generation != generation ||
		location == (taskstate.RemoteLease{}) || location.Validate() != nil ||
		location.PrimaryRoot != server.config.Root || location.Machine != worker.Machine ||
		location.Repository != snapshot.Definition.Repo || location.ProjectPath() != worker.Project ||
		lease.Path != worker.Worktree || server.tunnels == nil {
		return workeraccess.ErrDenied
	}
	if grant == nil && !partial {
		return workeraccess.ErrDenied
	}
	if grant != nil &&
		(!grant.Revoked || grant.Validate() != nil || grant.Task != taskID || grant.Generation != generation) {
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
			Task: taskID, Location: location, Holder: lease.Holder,
			Branch: lease.Branch, BaseCommit: lease.BaseCommit,
		},
		Lease: lease, Generation: generation,
	}
	tools, err := request.Tools()
	if err != nil {
		return err
	}
	if err := locked.CheckCurrent(); err != nil {
		return err
	}
	if grant == nil {
		// Activation publishes the grant before any remote credential or tunnel
		// effect. Only the primary staging secret can exist before publication.
		stage, err := remoteworker.OpenPrimaryCredentialStage(ctx, server.config.Root, request)
		if err != nil {
			return err
		}
		secret, err := stage.ReadSecret()
		if err == nil && len(secret) != 0 {
			digest := sha256.Sum256(secret)
			err = stage.RemoveSecret(hex.EncodeToString(digest[:]))
		}
		return errors.Join(err, stage.Close())
	}
	revoke := server.tunnels.Revoke
	if partial {
		revoke = server.tunnels.RevokePartialLaunch
	}
	if err := revoke(ctx, remoteworker.SocketOwner{
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
