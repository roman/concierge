package remoteworker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

// ExportResult completes SSH into a private spool before exposing bundle bytes.
// A lost connection cannot be mistaken for a confirmed result by the importer.
func (transport WorktreeTransport) ExportResult(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeResultRequest,
	output io.Writer,
) (WorktreeResult, error) {
	runner, ok := transport.Runner.(sshcommand.StreamingRunner)
	if !ok || output == nil || request.Validate() != nil || !profile.Enabled ||
		profile.ID != request.Worktree.Location.Machine || profile.Target != request.Worktree.Location.Target {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		profile.Target,
		request.Worktree.Location.Authentication,
		request.Worktree.Location.ClientPath(),
		"worker-worktree",
		"export-result",
	)
	if err != nil {
		return WorktreeResult{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return WorktreeResult{}, err
	}
	file, err := os.CreateTemp("", "concierge-remote-result-*")
	if err != nil {
		return WorktreeResult{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	diagnostics, err := runner.RunTo(ctx, arguments, bytes.NewReader(data), file)
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return WorktreeResult{}, err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	header, err := reader.ReadSlice('\n')
	if err != nil {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	var result WorktreeResult
	if decodeSetupRequest(bytes.NewReader(header), &result) != nil ||
		result.Lease != request.Lease ||
		result.Commit == "" {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	if request.Commit != "" && request.Commit != result.Commit {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	request.Commit = result.Commit
	if request.Validate() != nil {
		return WorktreeResult{}, ErrWorktreeOwnership
	}
	if _, err := io.Copy(output, reader); err != nil {
		return WorktreeResult{}, err
	}
	return result, nil
}
