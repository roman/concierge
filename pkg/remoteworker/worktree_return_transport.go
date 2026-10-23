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
		return WorktreeReturnRequest{}, ErrReturnRequest
	}
	return request, nil
}

func (transport WorktreeTransport) Return(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeReturnRequest,
) (taskstate.WorktreeLease, error) {
	location := request.Worktree.Location
	if request.Validate() != nil {
		return taskstate.WorktreeLease{}, ErrReturnRequest
	}
	if !profile.Enabled || profile.ID != location.Machine || profile.Target != location.Target {
		return taskstate.WorktreeLease{}, returnError(ErrReturnJournal, ErrWorktreeOwnership)
	}
	if transport.Runner == nil {
		return taskstate.WorktreeLease{}, ErrReturnTransport
	}
	helper := location
	if transport.ReturnClientCommit != "" {
		helper.ClientCommit = transport.ReturnClientCommit
		if helper.Validate() != nil {
			return taskstate.WorktreeLease{}, ErrReturnRequest
		}
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		profile.Target, location.Authentication, helper.ClientPath(),
		"worker-worktree", "return", "--protocol", "1",
	)
	if err != nil {
		return taskstate.WorktreeLease{}, returnError(ErrReturnTransport, err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return taskstate.WorktreeLease{}, returnError(ErrReturnRequest, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, diagnostics, runErr := transport.Runner.Run(ctx, arguments, bytes.NewReader(data))
	if ctx.Err() != nil {
		return taskstate.WorktreeLease{}, transport.returnFailure(
			returnError(ErrReturnTransport, ctx.Err()), diagnostics)
	}
	var response WorktreeReturnResponse
	if decodeSetupRequest(bytes.NewReader(output), &response) != nil {
		// Older helpers report command and decoding errors through the public CLI.
		// An arbitrary command failure does not establish lease ownership loss.
		if legacy := legacyReturnError(output); legacy != nil {
			return taskstate.WorktreeLease{}, transport.returnFailure(legacy, diagnostics)
		}
		if runErr != nil {
			return taskstate.WorktreeLease{}, transport.returnFailure(
				ErrReturnTransport,
				diagnostics,
			)
		}
		return taskstate.WorktreeLease{}, transport.returnFailure(ErrReturnValidation, diagnostics)
	}
	if validateRemovedPaths(request.Lease.Path, response.RemovedPaths) != nil {
		return taskstate.WorktreeLease{}, transport.returnFailure(ErrReturnValidation, diagnostics)
	}
	if response.Failure != "" {
		cause := response.failureCause()
		if cause != ErrReturnValidation {
			transport.reportReturnRemovals(response.RemovedPaths)
		}
		return taskstate.WorktreeLease{}, transport.returnFailure(cause,
			append(append([]byte(response.Diagnostics), '\n'), diagnostics...))
	}
	if response.Error != "" {
		transport.reportReturnRemovals(response.RemovedPaths)
		return taskstate.WorktreeLease{}, transport.returnFailure(
			legacyReturnCause(response.Error),
			diagnostics,
		)
	}
	if runErr != nil || len(diagnostics) != 0 {
		return taskstate.WorktreeLease{}, transport.returnFailure(ErrReturnTransport, diagnostics)
	}
	expected := request.Lease
	expected.State = taskstate.LeaseReturned
	if response.WorktreeLease != expected || response.Diagnostics != "" {
		return taskstate.WorktreeLease{}, transport.returnFailure(ErrReturnValidation, nil)
	}
	transport.reportReturnRemovals(response.RemovedPaths)
	return response.WorktreeLease, nil
}

func (transport WorktreeTransport) reportReturnRemovals(paths []string) {
	for _, path := range paths {
		if transport.RemovedPath != nil {
			transport.RemovedPath(path)
		}
	}
}

func legacyReturnError(output []byte) error {
	if len(output) > 64<<10 {
		return nil
	}
	line, _, _ := strings.Cut(string(output), "\n")
	var message string
	if !strings.HasPrefix(line, "error: ") ||
		json.Unmarshal([]byte(strings.TrimPrefix(line, "error: ")), &message) != nil {
		return nil
	}
	return legacyReturnCause(message)
}

func legacyReturnCause(message string) error {
	for _, known := range []error{ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
		if message == known.Error() {
			return returnError(ErrReturnProcesses, known)
		}
	}
	if message == ErrWorktreeOwnership.Error() {
		return returnError(ErrReturnJournal, ErrWorktreeOwnership)
	}
	if strings.HasPrefix(message, worktree.ErrIdentity.Error()+": ") {
		return fmt.Errorf("%w: %s", ErrReturnContents, launchDiagnostics([]byte(message)))
	}
	return fmt.Errorf("%w: %s", ErrReturnRequest, launchDiagnostics([]byte(message)))
}
