package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/gitremote"
)

// VerifyResultLanded confirms ancestry or the matching merged pull request.
func (client Client) VerifyResultLanded(
	ctx context.Context,
	project, commit, head, target string,
) error {
	branch, err := client.FetchBranch(ctx, project, target)
	if err != nil {
		return err
	}
	err = client.VerifyLanded(ctx, project, commit, branch)
	if !errors.Is(err, ErrResultNotLanded) {
		return err
	}
	origin, originErr := client.git(ctx, project, "config", "--get", "remote.origin.url")
	if originErr != nil {
		return originErr
	}
	repository, parseErr := gitremote.Repository(strings.TrimSpace(origin))
	if parseErr != nil {
		return err
	}
	merged, queryErr := mergedResultCommit(ctx, project, repository, head, branch.Name, commit)
	if queryErr != nil {
		return queryErr
	}
	return client.VerifyLanded(ctx, project, merged, branch)
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
		return "", ErrResultNotLanded
	}
	request := requests[0]
	if request.State != "MERGED" || request.HeadRefOid != selected || request.HeadRefName != head ||
		request.BaseRefName != base || request.MergeCommit.Oid == "" {
		return "", ErrResultNotLanded
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
