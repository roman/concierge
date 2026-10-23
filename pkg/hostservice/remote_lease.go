package hostservice

import (
	"context"
	"fmt"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
)

type hostRemoteLeases struct{ server *Server }

func (leases hostRemoteLeases) Return(
	ctx context.Context,
	task taskstate.Task,
	generation string,
	lease taskstate.WorktreeLease,
	reportRemovedPath func(string),
) (taskstate.WorktreeLease, error) {
	current, found := task.RetiredLease(generation)
	if !found || current != lease || lease.Remote.Repository != task.Definition().Repo {
		return taskstate.WorktreeLease{}, taskstate.ErrInvalidTransition
	}
	profile, err := leases.profile(ctx, lease.Remote)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	proof, err := leases.resultReturnProof(ctx, task, generation, lease, profile)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	location := lease.Remote
	location.Backend = ""
	request := remoteworker.WorktreeReturnRequest{
		Worktree: remoteworker.WorktreeRequest{
			Task: task.ID(), Location: location, Holder: lease.Holder,
			Branch: lease.Branch, BaseCommit: lease.BaseCommit,
		},
		Lease: lease,
		Proof: proof,
	}
	if leases.server.config.WorkerSettings != nil {
		settings, err := leases.server.config.WorkerSettings(task.Definition().Repo)
		if err != nil {
			return taskstate.WorktreeLease{}, err
		}
		request.ReturnIgnoredPaths = settings.ReturnIgnoredPaths
	}
	transport := leases.transport()
	transport.RemovedPath = reportRemovedPath
	client, err := (machineprep.Preparer{Runner: transport.Runner}).ReadClientAt(
		ctx, profile, location.Base, location.Authentication,
	)
	if err != nil {
		return taskstate.WorktreeLease{}, fmt.Errorf("%w: %w", remoteworker.ErrReturnRequest, err)
	}
	transport.ReturnClientCommit = client.Commit
	return transport.Return(ctx, profile, request)
}

func (leases hostRemoteLeases) InspectAbandoned(
	ctx context.Context,
	taskID string,
	receipt taskstate.LaunchAbandonment,
) (taskstate.WorktreeLease, error) {
	profile, err := leases.profile(ctx, receipt.Remote)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	return leases.transport().Inspect(ctx, profile, remoteworker.WorktreeRequest{
		Task: taskID, Location: receipt.Remote, Holder: receipt.LeaseHolder,
		Branch: receipt.Branch, BaseCommit: receipt.BaseCommit,
	})
}

func (leases hostRemoteLeases) profile(
	ctx context.Context,
	location taskstate.RemoteLease,
) (herdr.SavedMachine, error) {
	if location == (taskstate.RemoteLease{}) || location.Validate() != nil ||
		location.PrimaryRoot != leases.server.config.Root {
		return herdr.SavedMachine{}, remoteworker.ErrWorktreeOwnership
	}
	machines := leases.server.preparationMachines
	if machines == nil {
		machines = herdr.MachineCatalog{
			Runner: herdr.NewClient(leases.server.config.HerdrExecutable).Runner,
		}
	}
	profile, err := machines.Resolve(ctx, location.Machine)
	if err != nil {
		return profile, err
	}
	if !profile.Enabled || profile.ID != location.Machine || profile.Target != location.Target {
		return profile, remoteworker.ErrWorktreeOwnership
	}
	return profile, nil
}

func (leases hostRemoteLeases) transport() remoteworker.WorktreeTransport {
	runner := leases.server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	return remoteworker.WorktreeTransport{
		Runner: runner, Diagnostics: preparationDiagnostics{leases.server},
	}
}
