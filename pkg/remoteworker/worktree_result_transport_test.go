package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type resultSSHFixture struct {
	t       *testing.T
	profile herdr.SavedMachine
	request WorktreeResultRequest
	header  WorktreeResult
	payload []byte
	failure error
}

func TestRemoteResultRejectsOversizedBundleAndRemovesSpool(t *testing.T) {
	_, held := remoteReturnFixture(t)
	request := WorktreeResultRequest{
		Worktree: held.Worktree,
		Lease:    held.Lease,
		Commit:   held.Worktree.BaseCommit,
	}
	profile := herdr.SavedMachine{
		ID:      held.Worktree.Location.Machine,
		Target:  held.Worktree.Location.Target,
		Enabled: true,
	}
	for _, size := range []int{16, 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("TMPDIR", directory)
			fixture := &resultSSHFixture{t: t, profile: profile, request: request,
				header: WorktreeResult{
					Lease:  held.Lease,
					Commit: request.Commit,
				}, payload: bytes.Repeat([]byte("x"), size)}
			var output bytes.Buffer
			_, err := (WorktreeTransport{Runner: fixture, MaxResultBytes: 16}).ExportResult(
				context.Background(),
				profile,
				request,
				&output,
			)
			if size == 16 && (err != nil || output.Len() != size) {
				t.Fatalf("bounded result: bytes=%d err=%v", output.Len(), err)
			}
			if size > 16 && (!errors.Is(err, sshcommand.ErrOutputLimit) || output.Len() != 0) {
				t.Fatalf("oversized result published: bytes=%d err=%v", output.Len(), err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("result spool retained: %v %v", entries, err)
			}
		})
	}
}

func (fixture *resultSSHFixture) Run(context.Context, []string, io.Reader) ([]byte, []byte, error) {
	fixture.t.Fatal("binary result used the control response buffer")
	return nil, nil, errors.New("unexpected control request")
}

func (fixture *resultSSHFixture) RunTo(
	ctx context.Context,
	arguments []string,
	input io.Reader,
	output io.Writer,
) ([]byte, error) {
	expected, err := sshcommand.TrustedBatchArguments(
		fixture.profile.Target,
		fixture.request.Worktree.Location.ClientPath(),
		"worker-worktree",
		"export-result",
	)
	if err != nil || !slices.Equal(arguments, expected) {
		fixture.t.Fatal("result export changed the captured SSH destination or command")
	}
	request, err := DecodeWorktreeResultRequest(input)
	if err != nil || request != fixture.request {
		fixture.t.Fatal("result export changed the original lease or selected commit")
	}
	if err := json.NewEncoder(output).Encode(fixture.header); err != nil {
		return nil, err
	}
	_, err = output.Write(fixture.payload)
	return nil, errors.Join(err, fixture.failure, ctx.Err())
}

func TestRemoteResultTransferStreamsOnlyConfirmedMatchingResponse(t *testing.T) {
	_, held := remoteReturnFixture(t)
	request := WorktreeResultRequest{
		Worktree: held.Worktree,
		Lease:    held.Lease,
		Commit:   held.Worktree.BaseCommit,
	}
	profile := herdr.SavedMachine{
		ID:      held.Worktree.Location.Machine,
		Target:  held.Worktree.Location.Target,
		Enabled: true,
	}
	fixture := &resultSSHFixture{t: t, profile: profile, request: request,
		header:  WorktreeResult{Lease: held.Lease, Commit: request.Commit},
		payload: bytes.Repeat([]byte{0, 255, 128, 1}, 128<<10)}
	transport := WorktreeTransport{Runner: fixture}
	var output bytes.Buffer
	result, err := transport.ExportResult(context.Background(), profile, request, &output)
	if err != nil || result != fixture.header || !bytes.Equal(output.Bytes(), fixture.payload) {
		t.Fatalf("binary result transfer: bytes=%d err=%v", output.Len(), err)
	}
	for _, mismatch := range []string{"generation scope", "commit", "mutable commit", "connection lost", "destination"} {
		t.Run(mismatch, func(t *testing.T) {
			changed := *fixture
			selected := profile
			switch mismatch {
			case "generation scope":
				changed.header.Lease.Holder = "another-holder"
			case "commit":
				changed.header.Commit = held.Worktree.Location.ClientCommit
			case "mutable commit":
				changed.header.Commit = "HEAD"
			case "connection lost":
				changed.failure = io.ErrUnexpectedEOF
			case "destination":
				selected.Target = "another-target"
			}
			var received bytes.Buffer
			_, err := (WorktreeTransport{Runner: &changed}).ExportResult(
				context.Background(),
				selected,
				request,
				&received,
			)
			if err == nil || received.Len() != 0 {
				t.Fatalf(
					"unconfirmed result reached the importer: bytes=%d err=%v",
					received.Len(),
					err,
				)
			}
		})
	}
}
