package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type landingPullRequest struct {
	Number                                                      int
	URL, HeadRefOid, HeadRefName, BaseRefName, MergeStateStatus string
	IsDraft                                                     bool
}

func (target landing) mergePullRequest(ctx context.Context) (landingResult, error) {
	result := landingResult{checkout: "skipped: PR delivery", remoteBranch: "preserved"}
	var err error
	target.repository, err = landingRepository(target.remote)
	if err != nil {
		return result, err
	}
	data, err := target.gh(
		ctx,
		"pr",
		"list",
		"--head",
		target.branch,
		"--state",
		"open",
		"--limit",
		"2",
		"--json",
		"number,url,headRefOid,headRefName,baseRefName,isDraft,mergeStateStatus",
	)
	if err != nil {
		return result, err
	}
	var requests []landingPullRequest
	if err := json.Unmarshal([]byte(data), &requests); err != nil {
		return result, err
	}
	if len(requests) != 1 {
		return result, errors.New("expected exactly one open PR for the task branch")
	}
	request := requests[0]
	if request.Number <= 0 || request.URL == "" || request.HeadRefOid != target.head ||
		request.HeadRefName != target.branch ||
		request.BaseRefName != target.defaultBranch ||
		request.IsDraft {
		return result, errors.New(
			"PR does not match the ready task branch and default branch, or is a draft",
		)
	}
	// BLOCKED includes missing required checks and pending review requirements.
	if request.MergeStateStatus != "CLEAN" && request.MergeStateStatus != "HAS_HOOKS" {
		return result, fmt.Errorf("PR is not ready to merge: %s", request.MergeStateStatus)
	}
	number := fmt.Sprint(request.Number)
	data, err = target.gh(ctx, "pr", "checks", number, "--required", "--json", "name,state,bucket")
	if err != nil {
		return result, err
	}
	var checks []struct{ Name, State, Bucket string }
	if err := json.Unmarshal([]byte(data), &checks); err != nil {
		return result, err
	}
	if checks == nil {
		return result, errors.New("required checks response is not an array")
	}
	for _, check := range checks {
		if check.Bucket != "pass" || check.State != "SUCCESS" {
			return result, fmt.Errorf("required check %q is not green: %s", check.Name, check.State)
		}
	}
	if _, err := target.gh(ctx, "pr", "merge", number, "--squash", "--match-head-commit", target.head); err != nil {
		return result, err
	}
	data, err = target.gh(ctx, "pr", "view", number, "--json", "state,mergeCommit")
	if err != nil {
		return result, fmt.Errorf("merge requested but outcome could not be confirmed: %w", err)
	}
	var confirmation struct {
		State       string
		MergeCommit struct{ Oid string }
	}
	if err := json.Unmarshal([]byte(data), &confirmation); err != nil {
		return result, err
	}
	if confirmation.State != "MERGED" || confirmation.MergeCommit.Oid == "" {
		return result, errors.New("PR merge is unconfirmed. Inspect the PR before retrying")
	}
	result.commit = confirmation.MergeCommit.Oid
	result.pullRequest = request.URL
	return result, nil
}

func (target landing) gh(ctx context.Context, args ...string) (string, error) {
	return runLandingCommand(
		ctx,
		target.worktree,
		"gh",
		append(args, "--repo", target.repository)...)
}

func landingRepository(remote string) (string, error) {
	if strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		remote = "ssh://" + strings.Replace(remote, ":", "/", 1)
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if (parsed.Scheme != "https" && parsed.Scheme != "ssh" && parsed.Scheme != "http") ||
		parsed.Host == "" || len(strings.Split(path, "/")) != 2 || strings.ContainsAny(path, " \t\n?#") {
		return "", errors.New("PR delivery requires an origin URL with a host and owner/repository")
	}
	return parsed.Host + "/" + path, nil
}
