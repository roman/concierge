package hostservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

func (server *Server) activationRequest(
	taskID, generation string,
	profile herdr.SavedMachine,
	tools remoteworker.Tools,
	task taskstate.Task,
) (remoteworker.LaunchSiteRequest, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if task.State() != taskstate.InFlight || snapshot.Completion != nil ||
		task.Definition().Hold != nil ||
		worker == nil ||
		worker.Machine == "" ||
		worker.Generation != generation ||
		worker.PendingAdoption ||
		worker.PendingGeneration != "" ||
		worker.PendingLaunch != "" {
		return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
	}
	if !profile.Enabled || profile.ID != worker.Machine || profile.Session != worker.Session {
		return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
	}
	lease := worker.Lease
	if intent := snapshot.LaunchIntent; intent != nil {
		if intent.Generation != generation ||
			(lease != (taskstate.WorktreeLease{}) && lease != intent.Lease) {
			return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
		}
		lease = intent.Lease
	}
	location := lease.Remote
	if location == (taskstate.RemoteLease{}) || location.PrimaryRoot != server.config.Root ||
		location.Machine != worker.Machine ||
		location.Target != profile.Target ||
		location.Repository != snapshot.Definition.Repo ||
		lease.Project != worker.Project ||
		lease.Path != worker.Worktree {
		return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
	}
	location.Backend = ""
	request := remoteworker.LaunchSiteRequest{
		Worktree: remoteworker.WorktreeRequest{
			Task:       taskID,
			Location:   location,
			Holder:     lease.Holder,
			Branch:     lease.Branch,
			BaseCommit: lease.BaseCommit,
		},
		Lease:      lease,
		Generation: generation,
	}
	expected, err := request.Tools()
	if err != nil {
		return remoteworker.LaunchSiteRequest{}, err
	}
	if tools != expected {
		return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
	}
	if access := worker.Access; access != nil &&
		(access.Validate() != nil || access.Revoked || access.Task != taskID || access.Generation != generation) {
		return remoteworker.LaunchSiteRequest{}, workeraccess.ErrDenied
	}
	return request, nil
}

// activateRemoteWorker holds task authority across each setup effect. A retained
// staging secret survives a crash before grant publication or remote delivery.
func (server *Server) activateRemoteWorker(
	ctx context.Context,
	taskID, generation string,
	profile herdr.SavedMachine,
	tools remoteworker.Tools,
) error {
	if server.tunnels == nil {
		return remoteworker.ErrTunnelUnreachable
	}
	runner := server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	_, err := server.store.WithTask(ctx, taskID, func(task *taskstate.LockedTask) (result error) {
		request, err := server.activationRequest(taskID, generation, profile, tools, task.Current())
		if err != nil {
			return err
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		stage, err := remoteworker.OpenPrimaryCredentialStage(ctx, server.config.Root, request)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, stage.Close()) }()
		staged, err := stage.ReadSecret()
		if err != nil {
			return err
		}
		var grant workeraccess.Grant
		if access := task.Current().Snapshot().Worker.Access; access != nil {
			grant = *access
		} else {
			if len(staged) == 0 {
				issued, secret, err := workeraccess.Issue(taskID, generation, workeraccess.Operations())
				if err != nil {
					return err
				}
				if err := stage.InstallSecret(secret); err != nil {
					return err
				}
				grant, staged = issued, secret.Bytes()
			} else {
				digest := sha256.Sum256(staged)
				grant = workeraccess.Grant{Task: taskID, Generation: generation, Operations: workeraccess.Operations(), Verifier: hex.EncodeToString(digest[:])}
			}
			if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) { return current.InstallWorkerAccess(grant) }); err != nil {
				return err
			}
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		if len(staged) != 0 {
			secret, err := grant.RestoreSecret(bytes.NewReader(staged))
			if err != nil {
				return err
			}
			if err := (remoteworker.SecretTransport{Runner: runner}).Install(ctx, profile, tools, secret); err != nil {
				return err
			}
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		binding := replyoutbox.Context{
			Format:     1,
			Root:       server.config.Root,
			Task:       taskID,
			Generation: generation,
			Recipient:  server.config.Recipient.String(),
			Verifier:   grant.Verifier,
		}
		if err := (remoteworker.ContextTransport{Runner: runner}).Install(ctx, profile, tools, binding); err != nil {
			return err
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		spec := remoteworker.TunnelSpec{
			Owner: remoteworker.SocketOwner{
				Task:       taskID,
				Generation: generation,
				Verifier:   grant.Verifier,
			},
			MachineID: profile.ID,
			Target:    profile.Target,
			Tools:     tools,
		}
		if err := server.tunnels.Ensure(ctx, spec); err != nil {
			return err
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		return stage.RemoveSecret(grant.Verifier)
	})
	return err
}
