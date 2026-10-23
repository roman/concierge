package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func DecodeWorktreeReturnRequest(input io.Reader) (WorktreeReturnRequest, error) {
	var request WorktreeReturnRequest
	if decodeSetupRequest(input, &request) != nil || request.Validate() != nil {
		return WorktreeReturnRequest{}, ErrWorktreeOwnership
	}
	return request, nil
}

func (transport WorktreeTransport) Return(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeReturnRequest,
) (taskstate.WorktreeLease, error) {
	location := request.Worktree.Location
	if transport.Runner == nil || request.Validate() != nil || !profile.Enabled ||
		profile.ID != location.Machine || profile.Target != location.Target {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		profile.Target,
		location.Authentication,
		location.ClientPath(),
		"worker-worktree",
		"return",
	)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, diagnostics, err := transport.Runner.Run(ctx, arguments, bytes.NewReader(data))
	var response WorktreeReturnResponse
	if ctx.Err() == nil && len(diagnostics) == 0 && len(output) <= 64<<10 &&
		decodeSetupRequest(bytes.NewReader(output), &response) == nil {
		if validateRemovedPaths(request.Lease.Path, response.RemovedPaths) != nil {
			return taskstate.WorktreeLease{}, ErrWorktreeOwnership
		}
		for _, path := range response.RemovedPaths {
			if transport.RemovedPath != nil {
				transport.RemovedPath(path)
			}
		}
		if response.Error != "" {
			for _, known := range []error{ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
				if response.Error == known.Error() {
					return taskstate.WorktreeLease{}, known
				}
			}
			return taskstate.WorktreeLease{}, fmt.Errorf(
				"%w: %s",
				ErrWorktreeOwnership,
				response.Error,
			)
		}
	}
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 || len(output) > 64<<10 {
		if ctx.Err() == nil && len(output) <= 64<<10 {
			for _, known := range []error{ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
				encoded, _ := json.Marshal(known.Error())
				if strings.Contains(string(output), "error: "+string(encoded)+"\n") {
					return taskstate.WorktreeLease{}, known
				}
			}
			line, _, _ := strings.Cut(string(output), "\n")
			var message string
			if strings.HasPrefix(line, "error: ") &&
				json.Unmarshal([]byte(strings.TrimPrefix(line, "error: ")), &message) == nil &&
				strings.HasPrefix(message, worktree.ErrIdentity.Error()+": ") {
				return taskstate.WorktreeLease{}, fmt.Errorf(
					"%w: %s",
					ErrWorktreeOwnership,
					message,
				)
			}
		}
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	expected := request.Lease
	expected.State = taskstate.LeaseReturned
	if decodeSetupRequest(bytes.NewReader(output), &response) != nil ||
		response.WorktreeLease != expected {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	return response.WorktreeLease, nil
}
