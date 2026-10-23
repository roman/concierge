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
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type resultSSHFixture struct {
	t         *testing.T
	profile   herdr.SavedMachine
	request   WorktreeResultRequest
	header    WorktreeResult
	payload   []byte
	failure   error
	rejection error
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
			result, err := (WorktreeTransport{Runner: fixture, MaxResultBytes: 16}).ExportResult(
				context.Background(),
				profile,
				request,
			)
			if size == 16 {
				if err != nil || result == nil {
					t.Fatalf("bounded result: %v", err)
				}
				data, err := io.ReadAll(result.Bundle)
				if err != nil || len(data) != size {
					t.Fatalf("bounded payload: bytes=%d err=%v", len(data), err)
				}
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 1 {
					t.Fatalf("expected one owned spool: %v %v", entries, err)
				}
				if err := result.Bundle.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if size > 16 && (!errors.Is(err, sshcommand.ErrOutputLimit) || result != nil) {
				t.Fatalf("oversized result published: result=%+v err=%v", result, err)
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
	if fixture.rejection != nil {
		encoded, _ := json.Marshal(fixture.rejection.Error())
		_, err := fmt.Fprintf(output, "error: %s\nhelp: \"retain the lease\"\n", encoded)
		return nil, errors.Join(err, errors.New("remote export rejected"))
	}
	if err := json.NewEncoder(output).Encode(fixture.header); err != nil {
		return nil, err
	}
	_, err = output.Write(fixture.payload)
	return nil, errors.Join(err, fixture.failure, ctx.Err())
}

func TestRemoteResultTransferRetainsExportFailureDetails(t *testing.T) {
	_, held := remoteReturnFixture(t)
	request := WorktreeResultRequest{Worktree: held.Worktree, Lease: held.Lease}
	profile := herdr.SavedMachine{
		ID:      held.Worktree.Location.Machine,
		Target:  held.Worktree.Location.Target,
		Enabled: true,
	}
	for _, rejection := range []error{
		fmt.Errorf("%w: ?? unfinished file.txt", ErrResultDirty),
		fmt.Errorf("%w: checkout HEAD differs from selected commit", ErrWorktreeOwnership),
		fmt.Errorf("%w: cannot create bundle", ErrResultExport),
	} {
		t.Run(rejection.Error(), func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("TMPDIR", directory)
			fixture := &resultSSHFixture{
				t:         t,
				profile:   profile,
				request:   request,
				rejection: rejection,
			}
			result, err := (WorktreeTransport{Runner: fixture}).ExportResult(
				t.Context(),
				profile,
				request,
			)
			if result != nil || !errors.Is(err, errors.Unwrap(rejection)) ||
				!strings.Contains(err.Error(), rejection.Error()) {
				t.Fatalf("lost export diagnosis: result=%v err=%v", result, err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed export retained spool: %v %v", entries, err)
			}
		})
	}
	fixture := &resultSSHFixture{
		t: t, profile: profile, request: request,
		rejection: errors.New("unknown remote failure with untrusted details"),
	}
	result, err := (WorktreeTransport{Runner: fixture}).ExportResult(t.Context(), profile, request)
	if result != nil || err != ErrWorktreeOwnership {
		t.Fatalf("forwarded an unknown remote diagnosis: result=%v err=%v", result, err)
	}
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
	result, err := transport.ExportResult(context.Background(), profile, request)
	if err != nil || result == nil {
		t.Fatalf("binary result transfer: %v", err)
	}
	defer result.Bundle.Close()
	output, err := io.ReadAll(result.Bundle)
	if err != nil || result.Result != fixture.header || !bytes.Equal(output, fixture.payload) {
		t.Fatalf("binary result transfer: bytes=%d err=%v", len(output), err)
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
			received, err := (WorktreeTransport{Runner: &changed}).ExportResult(
				context.Background(),
				selected,
				request,
			)
			if err == nil || received != nil {
				t.Fatalf(
					"unconfirmed result reached the importer: result=%+v err=%v",
					received,
					err,
				)
			}
		})
	}
}
