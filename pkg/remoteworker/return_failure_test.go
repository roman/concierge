package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type returnFailureSSH struct {
	output, stderr []byte
	err            error
}

func TestReturnFailuresKeepStagesAndRedactAtSource(t *testing.T) {
	t.Setenv("RETURN_FAILURE_SECRET", "private-stage-secret")
	for _, stage := range []returnStage{ErrReturnRequest, ErrReturnJournal, ErrReturnProcesses,
		ErrReturnFsmonitor, ErrReturnContents, ErrReturnPool, ErrReturnTransport, ErrReturnValidation} {
		t.Run(string(stage), func(t *testing.T) {
			response := WorktreeReturnResponse{}
			response.SetFailure(
				returnError(
					stage,
					errors.New("detail private-stage-secret "+strings.Repeat("x", 4096)),
				),
			)
			if !errors.Is(response.failureCause(), stage) ||
				errors.Is(response.failureCause(), ErrWorktreeOwnership) {
				t.Fatalf("wrong stage: %+v", response)
			}
			encoded, _ := json.Marshal(response)
			if strings.Contains(string(encoded), "private-stage-secret") ||
				len(response.Diagnostics) > 2060 ||
				strings.Contains(response.Error, "detail") {
				t.Fatal("dependency details escaped the bounded diagnostics field")
			}
		})
	}
}

func TestReturnManagerDistinguishesJournalProcessesCleanupAndPool(t *testing.T) {
	for _, stage := range []returnStage{ErrReturnJournal, ErrReturnProcesses, ErrReturnContents, ErrReturnPool} {
		t.Run(string(stage), func(t *testing.T) {
			manager, request := remoteReturnFixture(t)
			switch stage {
			case ErrReturnJournal:
				request.Lease.ID = "foreign"
			case ErrReturnProcesses:
				manager.CheckQuiet = func(context.Context, string) error { return ErrWorktreeBusy }
			case ErrReturnContents:
				if err := os.WriteFile(filepath.Join(request.Lease.Path, "untracked"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case ErrReturnPool:
				wrapper := filepath.Join(t.TempDir(), "git")
				script := "#!/bin/sh\ncase \"$*\" in *'worktree remove'*) echo 'pool remove refused' >&2; exit 1;; esac\nexec '" + manager.Git.Executable + "' \"$@\"\n"
				if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				manager.Git.Executable = wrapper
			}
			_, err := manager.Return(t.Context(), request)
			if !errors.Is(err, stage) ||
				errors.Is(err, ErrWorktreeOwnership) != (stage == ErrReturnJournal) {
				t.Fatalf("wrong manager failure: %v", err)
			}
			response := WorktreeReturnResponse{}
			response.SetFailure(err)
			if !errors.Is(response.failureCause(), stage) ||
				errors.Is(
					response.failureCause(),
					ErrWorktreeOwnership,
				) != (stage == ErrReturnJournal) {
				t.Fatalf("wrong wire failure: %+v", response)
			}
		})
	}
}

func (runner returnFailureSSH) Run(context.Context, []string, io.Reader) ([]byte, []byte, error) {
	return runner.output, runner.stderr, runner.err
}

func TestReturnTransportFailureIsNotOwnership(t *testing.T) {
	_, request := remoteReturnFixture(t)
	t.Setenv("RETURN_TEST_TOKEN", "private-return-test-token")
	for _, failure := range []struct {
		name   string
		runner returnFailureSSH
		want   string
	}{
		{"transport", returnFailureSSH{stderr: []byte("SSH denied private-return-test-token" + strings.Repeat("x", 4096)), err: errors.New("ssh failed")}, "transport"},
		{"response", returnFailureSSH{output: []byte(`{"unexpected":true}`)}, "response validation"},
		{"protocol", returnFailureSSH{output: []byte("error: \"unknown flag --protocol\"\n"), err: errors.New("helper failed")}, "request/protocol"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			var diagnostics bytes.Buffer
			transport := WorktreeTransport{Runner: failure.runner, Diagnostics: &diagnostics}
			profile := herdr.SavedMachine{ID: request.Worktree.Location.Machine,
				Target: request.Worktree.Location.Target, Enabled: true}
			_, err := transport.Return(t.Context(), profile, request)
			if err == nil || errors.Is(err, ErrWorktreeOwnership) ||
				!strings.Contains(err.Error(), failure.want) {
				t.Fatalf("wrong failure stage: %v", err)
			}
			if strings.Contains(diagnostics.String(), "private-return-test-token") ||
				diagnostics.Len() > 2300 {
				t.Fatalf("unbounded or unredacted host log: %q", diagnostics.String())
			}
			if failure.name == "transport" &&
				!strings.Contains(diagnostics.String(), "SSH denied") {
				t.Fatal("stderr absent from the host log")
			}
		})
	}
}

func TestReturnTransportPreservesRemoteStageAndPrivateDiagnostics(t *testing.T) {
	_, request := remoteReturnFixture(t)
	t.Setenv("RETURN_WIRE_SECRET", "private-wire-secret")
	for _, stage := range []returnStage{ErrReturnRequest, ErrReturnJournal, ErrReturnProcesses,
		ErrReturnFsmonitor, ErrReturnContents, ErrReturnPool} {
		t.Run(string(stage), func(t *testing.T) {
			response := WorktreeReturnResponse{}
			response.SetFailure(
				returnError(stage, errors.New("dependency failed private-wire-secret")),
			)
			data, _ := json.Marshal(response)
			var log bytes.Buffer
			transport := WorktreeTransport{
				Runner:      returnFailureSSH{output: data, err: errors.New("exit status 1")},
				Diagnostics: &log,
			}
			profile := herdr.SavedMachine{ID: request.Worktree.Location.Machine,
				Target: request.Worktree.Location.Target, Enabled: true}
			_, err := transport.Return(t.Context(), profile, request)
			if !errors.Is(err, stage) || errors.Is(err, ErrWorktreeOwnership) ||
				strings.Contains(err.Error(), "dependency") ||
				!strings.Contains(log.String(), "dependency failed") ||
				strings.Contains(log.String(), "private-wire-secret") {
				t.Fatalf("remote failure: %v; host log: %s", err, &log)
			}
		})
	}
}
