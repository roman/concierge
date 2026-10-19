package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/worktree"
)

const DefaultMaxResultBytes int64 = 1 << 30

type CompletedResult struct {
	Result WorktreeResult
	Bundle *worktree.CommitBundle
}

// ExportResult publishes bytes only after a bounded, validated SSH transfer.
func (transport WorktreeTransport) ExportResult(
	ctx context.Context,
	profile herdr.SavedMachine,
	request WorktreeResultRequest,
) (*CompletedResult, error) {
	runner, ok := transport.Runner.(sshcommand.StreamingRunner)
	if !ok || request.Validate() != nil || !profile.Enabled ||
		profile.ID != request.Worktree.Location.Machine || profile.Target != request.Worktree.Location.Target ||
		transport.MaxResultBytes < 0 {
		return nil, ErrWorktreeOwnership
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		profile.Target,
		request.Worktree.Location.Authentication,
		request.Worktree.Location.ClientPath(),
		"worker-worktree",
		"export-result",
	)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "concierge-remote-result-*")
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	limit := transport.MaxResultBytes
	if limit == 0 {
		limit = DefaultMaxResultBytes
	}
	download := resultDownload{request: request, output: file, remaining: limit}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	diagnostics, err := runner.RunTo(ctx, arguments, bytes.NewReader(data), &download)
	if errors.Is(err, sshcommand.ErrOutputLimit) {
		return nil, err
	}
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 || download.result.Commit == "" {
		return nil, ErrWorktreeOwnership
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	complete = true
	return &CompletedResult{
		Result: download.result,
		Bundle: worktree.AdoptCommitBundle(file, download.result.Commit),
	}, nil
}

type resultDownload struct {
	request   WorktreeResultRequest
	output    io.Writer
	header    []byte
	result    WorktreeResult
	remaining int64
}

func (download *resultDownload) Write(data []byte) (int, error) {
	consumed := 0
	if download.result.Commit == "" {
		end := bytes.IndexByte(data, '\n')
		length := len(data)
		if end >= 0 {
			length = end + 1
		}
		if len(download.header)+length > 64<<10 {
			return 0, sshcommand.ErrOutputLimit
		}
		download.header = append(download.header, data[:length]...)
		if end < 0 {
			return length, nil
		}
		var result WorktreeResult
		if decodeSetupRequest(bytes.NewReader(download.header), &result) != nil ||
			result.Lease != download.request.Lease || result.Commit == "" ||
			(download.request.Commit != "" && download.request.Commit != result.Commit) {
			return 0, ErrWorktreeOwnership
		}
		request := download.request
		request.Commit = result.Commit
		if request.Validate() != nil {
			return 0, ErrWorktreeOwnership
		}
		download.result, download.header = result, nil
		consumed, data = length, data[length:]
	}
	if int64(len(data)) > download.remaining {
		return consumed, sshcommand.ErrOutputLimit
	}
	n, err := download.output.Write(data)
	download.remaining -= int64(n)
	return consumed + n, err
}
