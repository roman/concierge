package worktree

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrResultNotLanded = errors.New("selected result is not on the fetched default branch")

type DefaultBranch struct{ Name, Tip string }

// VerifyUnlanded requires committed work that is absent from the fetched target.
func (client Client) VerifyUnlanded(ctx context.Context, specification Specification) error {
	if err := client.VerifyIdentity(ctx, specification); err != nil {
		return err
	}
	branch, err := client.FetchDefaultBranch(ctx, specification.Project)
	if err != nil {
		return err
	}
	head, err := client.git(ctx, specification.Path, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	err = client.VerifyLanded(ctx, specification.Project, strings.TrimSpace(head), branch)
	if errors.Is(err, ErrResultNotLanded) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("project work is already landed; create a new task")
}

func immutableCommitID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && (len(decoded) == 20 || len(decoded) == 32) &&
		strings.ToLower(value) == value
}

// FetchDefaultBranch refreshes landing evidence without moving an operator
// checkout or changing FETCH_HEAD shared by other primary operations.
func (client Client) FetchDefaultBranch(
	ctx context.Context,
	project string,
) (DefaultBranch, error) {
	if !filepath.IsAbs(client.Executable) || !filepath.IsAbs(project) {
		return DefaultBranch{}, ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	advertised, err := client.git(ctx, project, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return DefaultBranch{}, err
	}
	var reference, tip string
	for _, line := range strings.Split(strings.TrimSpace(advertised), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/"):
			if reference != "" {
				return DefaultBranch{}, ErrIdentity
			}
			reference = fields[1]
		case len(fields) == 2 && fields[1] == "HEAD" && immutableCommitID(fields[0]):
			if tip != "" {
				return DefaultBranch{}, ErrIdentity
			}
			tip = fields[0]
		default:
			return DefaultBranch{}, ErrIdentity
		}
	}
	if reference == "" || tip == "" {
		return DefaultBranch{}, ErrIdentity
	}
	if _, err := client.git(ctx, project, "check-ref-format", reference); err != nil {
		return DefaultBranch{}, err
	}
	name := strings.TrimPrefix(reference, "refs/heads/")
	tracking := "refs/remotes/origin/" + name
	if _, err := client.git(ctx, project, "fetch", "--no-tags", "--no-write-fetch-head", "origin", reference+":"+tracking); err != nil {
		return DefaultBranch{}, err
	}
	current, err := client.git(ctx, project, "rev-parse", "--verify", tracking+"^{commit}")
	if err != nil || !immutableCommitID(strings.TrimSpace(current)) {
		return DefaultBranch{}, ErrIdentity
	}
	return DefaultBranch{Name: name, Tip: strings.TrimSpace(current)}, nil
}

func (client Client) VerifyLanded(
	ctx context.Context,
	project, commit string,
	branch DefaultBranch,
) error {
	if !filepath.IsAbs(client.Executable) || !filepath.IsAbs(project) ||
		!immutableCommitID(commit) ||
		!immutableCommitID(branch.Tip) {
		return ErrIdentity
	}
	_, err := client.git(ctx, project, "merge-base", "--is-ancestor", commit, branch.Tip)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return ErrResultNotLanded
	}
	return err
}
