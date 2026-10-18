package hostservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/roman/concierge/pkg/gitremote"
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
	if !found || result.Commit == lease.BaseCommit {
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
	branch, err := git.FetchDefaultBranch(ctx, project)
	if err != nil {
		return remoteworker.WorktreeReturnProof{}, err
	}
	if err = git.VerifyLanded(ctx, project, result.Commit, branch); errors.Is(
		err,
		worktree.ErrResultNotLanded,
	) {
		repository, parseErr := gitremote.Repository(origin)
		if parseErr != nil {
			return remoteworker.WorktreeReturnProof{}, err
		}
		merged, queryErr := mergedResultCommit(
			ctx,
			project,
			repository,
			lease.Branch,
			branch.Name,
			result.Commit,
		)
		if queryErr != nil {
			return remoteworker.WorktreeReturnProof{}, queryErr
		}
		err = git.VerifyLanded(ctx, project, merged, branch)
	}
	if err != nil {
		return remoteworker.WorktreeReturnProof{}, err
	}
	return remoteworker.WorktreeReturnProof{
		Outcome: remoteworker.WorktreeLanded,
		Commit:  result.Commit,
	}, nil
}

func mergedResultCommit(
	ctx context.Context,
	project, repository, head, base, selected string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"gh",
		"pr",
		"list",
		"--repo",
		repository,
		"--head",
		head,
		"--base",
		base,
		"--state",
		"merged",
		"--limit",
		"2",
		"--json",
		"state,headRefOid,headRefName,baseRefName,mergeCommit",
	)
	command.Dir, command.Stderr, command.WaitDelay = project, io.Discard, time.Second
	output := mergedPRResponse{cancel: cancel}
	command.Stdout = &output
	if err := command.Run(); err != nil || ctx.Err() != nil {
		return "", errors.New(
			"merged PR confirmation is unavailable. Check gh auth status on the primary and inspect the task PR",
		)
	}
	var requests []struct {
		State, HeadRefOid, HeadRefName, BaseRefName string
		MergeCommit                                 struct{ Oid string }
	}
	if json.Unmarshal(output.data.Bytes(), &requests) != nil || len(requests) != 1 {
		return "", worktree.ErrResultNotLanded
	}
	request := requests[0]
	if request.State != "MERGED" || request.HeadRefOid != selected || request.HeadRefName != head ||
		request.BaseRefName != base || request.MergeCommit.Oid == "" {
		return "", worktree.ErrResultNotLanded
	}
	return request.MergeCommit.Oid, nil
}

type mergedPRResponse struct {
	data   bytes.Buffer
	cancel context.CancelFunc
}

func (response *mergedPRResponse) Write(data []byte) (int, error) {
	if len(data) > (64<<10)-response.data.Len() {
		response.cancel()
		return 0, errors.New("merged PR response exceeds 64 KiB")
	}
	return response.data.Write(data)
}
