package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/roman/concierge/pkg/gitremote"
	"github.com/roman/concierge/pkg/taskstate"
)

// Remote workers retain local commits. Only the primary publishes their branch
// and creates a PR before applying the existing merge and required-check rules.
func (target landing) prepareRemotePullRequest(
	ctx context.Context,
	definition taskstate.Definition,
) error {
	remote, err := matchingLandingEndpoints(ctx, target.project)
	if err != nil {
		return err
	}
	if remote != target.remote {
		return errors.New("origin changed during remote PR preparation")
	}
	repository, err := gitremote.Repository(remote)
	if err != nil {
		return err
	}
	target.repository = repository
	reference := "refs/heads/" + target.branch
	advertised, err := landingGitCommand(
		ctx,
		target.project,
		"ls-remote",
		"--heads",
		"origin",
		reference,
	)
	if err != nil {
		return err
	}
	if advertised != "" {
		fields := strings.Fields(advertised)
		if len(fields) != 2 || fields[0] != target.head || fields[1] != reference {
			return errors.New(
				"remote task branch differs from the selected result; preserve the existing branch",
			)
		}
	} else {
		// An empty explicit lease creates only an absent branch, including races.
		if _, err := landingGitCommand(ctx, target.project, "push", "--force-with-lease="+reference+":", "origin", target.head+":"+reference); err != nil {
			return err
		}
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
		"number",
	)
	if err != nil {
		return err
	}
	var requests []struct{ Number int }
	if err := json.Unmarshal([]byte(data), &requests); err != nil {
		return err
	}
	if requests == nil {
		return errors.New("PR lookup response is not an array")
	}
	if len(requests) != 0 {
		return nil
	}
	body, err := os.CreateTemp("", "concierge-remote-pr-*")
	if err != nil {
		return err
	}
	defer os.Remove(body.Name())
	_, writeErr := fmt.Fprintf(
		body,
		"Task: %s\n\nAccepted commit: %s\n",
		definition.ID,
		target.head,
	)
	if err := body.Close(); writeErr != nil || err != nil {
		return errors.Join(writeErr, err)
	}
	_, err = target.gh(ctx, "pr", "create", "--base", target.defaultBranch, "--head", target.branch,
		"--title", definition.Title, "--body-file", body.Name())
	return err
}
