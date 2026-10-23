package hostservice

import (
	"context"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func (leases hostRemoteLeases) resultReturnProof(
	ctx context.Context,
	task taskstate.Task,
	generation string,
	lease taskstate.WorktreeLease,
	profile herdr.SavedMachine,
) (remoteworker.WorktreeReturnProof, error) {
	proof := remoteworker.WorktreeReturnProof{
		Outcome: remoteworker.WorktreeUnchanged,
		Commit:  lease.BaseCommit,
	}
	if _, abandoned := task.LaunchAbandonment(generation); abandoned {
		proof.Outcome = remoteworker.WorktreeAbandoned
		return proof, nil
	}
	result, found := task.RetiredRemoteResult(generation)
	if task.HasReceipt(generation, taskstate.Retain) {
		// Retained receipts release leases only through an explicit primary request.
		proof.Outcome = remoteworker.WorktreeAbandoned
		if found {
			proof.Commit = result.Commit
		}
		return proof, nil
	}
	if !task.HasReceipt(generation, taskstate.Close) {
		return remoteworker.WorktreeReturnProof{}, taskstate.ErrInvalidTransition
	}
	if task.Definition().Kind == "scout" {
		proof.Outcome = remoteworker.WorktreeScout
		return proof, nil
	}
	if !found {
		return proof, nil
	}
	configuration, err := readMachineConfiguration(leases.server.config.Root, profile)
	if err != nil {
		return remoteworker.WorktreeReturnProof{}, err
	}
	origin := ""
	for _, repository := range configuration.Repositories {
		if repository.Name == task.Definition().Repo {
			origin = repository.URL
		}
	}
	if origin == "" {
		return remoteworker.WorktreeReturnProof{}, remoteworker.ErrWorktreeOwnership
	}
	project := lease.Remote.PrimaryProject
	if err := leases.server.checkRemoteLaunchOrigin(ctx, project, origin); err != nil {
		return remoteworker.WorktreeReturnProof{}, err
	}
	git := worktree.Client{Executable: leases.server.config.GitExecutable}
	targetBranch := ""
	if leases.server.config.WorkerSettings != nil {
		settings, err := leases.server.config.WorkerSettings(task.Definition().Repo)
		if err != nil {
			return remoteworker.WorktreeReturnProof{}, err
		}
		targetBranch = settings.Branch
	}
	if err := git.VerifyResultLanded(ctx, project, result.Commit, lease.Branch, targetBranch); err != nil {
		return remoteworker.WorktreeReturnProof{}, err
	}
	return remoteworker.WorktreeReturnProof{
		Outcome: remoteworker.WorktreeLanded,
		Commit:  result.Commit,
	}, nil
}
