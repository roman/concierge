package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
)

type WorktreeTransport struct{ Runner sshcommand.Runner }

// DecodeWorktreeRequest accepts only the bounded native setup protocol.
func DecodeWorktreeRequest(input io.Reader) (WorktreeRequest, error) {
	var request WorktreeRequest
	if decodeSetupRequest(input, &request) != nil || request.Validate() != nil {
		return WorktreeRequest{}, ErrWorktreeOwnership
	}
	return request, nil
}

func decodeSetupRequest(input io.Reader, destination any) error {
	if input == nil {
		return ErrWorktreeOwnership
	}
	data, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return ErrWorktreeOwnership
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return ErrWorktreeOwnership
	}
	return nil
}

func (transport WorktreeTransport) Acquire(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeRequest,
) (taskstate.WorktreeLease, error) {
	return transport.requestLease(ctx, profile, request, "acquire")
}

func (transport WorktreeTransport) Inspect(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeRequest,
) (taskstate.WorktreeLease, error) {
	return transport.requestLease(ctx, profile, request, "inspect")
}

func (transport WorktreeTransport) requestLease(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeRequest,
	operation string,
) (taskstate.WorktreeLease, error) {
	if transport.Runner == nil || request.Validate() != nil || !profile.Enabled ||
		profile.ID != request.Location.Machine ||
		profile.Target != request.Location.Target {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	arguments, err := sshcommand.TrustedBatchArguments(
		profile.Target,
		request.Location.ClientPath(),
		"worker-worktree",
		operation,
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
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 || len(output) > 64<<10 {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var lease taskstate.WorktreeLease
	if decoder.Decode(&lease) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	record := worktreeRecord{
		Format:  1,
		Request: request,
		Backend: lease.Remote.Backend,
		Phase:   worktreeReady,
		Lease:   lease,
	}
	if record.validate() != nil {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	return lease, nil
}
