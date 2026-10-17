package remoteworker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

// DecodeBaseImport separates one bounded JSON header from the binary bundle.
func DecodeBaseImport(input io.Reader) (WorktreeRequest, io.Reader, error) {
	if input == nil {
		return WorktreeRequest{}, nil, ErrWorktreeOwnership
	}
	reader := bufio.NewReaderSize(input, 64<<10)
	header, err := reader.ReadSlice('\n')
	if err != nil {
		return WorktreeRequest{}, nil, ErrWorktreeOwnership
	}
	request, err := DecodeWorktreeRequest(bytes.NewReader(header))
	if err != nil {
		return WorktreeRequest{}, nil, err
	}
	return request, reader, nil
}

func (transport WorktreeTransport) ImportBase(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeRequest,
	bundle io.Reader,
) error {
	if transport.Runner == nil || bundle == nil || request.Validate() != nil || !profile.Enabled ||
		profile.ID != request.Location.Machine ||
		profile.Target != request.Location.Target {
		return ErrWorktreeOwnership
	}
	arguments, err := sshcommand.TrustedBatchArguments(
		profile.Target,
		request.Location.ClientPath(),
		"worker-worktree",
		"import",
	)
	if err != nil {
		return err
	}
	header, err := json.Marshal(request)
	if err != nil {
		return err
	}
	header = append(header, '\n')
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, diagnostics, err := transport.Runner.Run(
		ctx,
		arguments,
		io.MultiReader(bytes.NewReader(header), bundle),
	)
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 ||
		strings.TrimSpace(string(output)) != "status: imported" {
		return ErrWorktreeOwnership
	}
	return nil
}
