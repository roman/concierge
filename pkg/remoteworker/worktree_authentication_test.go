package remoteworker

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type worktreeAuthenticationRunner struct {
	t     *testing.T
	mode  sshcommand.Authentication
	calls int
}

func (runner *worktreeAuthenticationRunner) Run(
	_ context.Context,
	arguments []string,
	_ io.Reader,
) ([]byte, []byte, error) {
	runner.calls++
	want := "ForwardAgent=no"
	if runner.mode == sshcommand.ForwardedAgent {
		want = "ForwardAgent=yes"
	}
	if !slices.Contains(arguments, want) {
		runner.t.Fatalf("captured authentication lost: mode=%s args=%v", runner.mode, arguments)
	}
	return nil, nil, errors.New("stop after authentication inspection")
}

func (runner *worktreeAuthenticationRunner) RunTo(
	ctx context.Context,
	arguments []string,
	input io.Reader,
	_ io.Writer,
) ([]byte, error) {
	_, _, err := runner.Run(ctx, arguments, input)
	return nil, err
}

func TestNativeWorktreeOperationsUseCapturedAuthentication(t *testing.T) {
	_, original := remoteReturnFixture(t)
	original.Worktree.Location.Base = "/r"
	original.Lease.Remote.Base = "/r"
	original.Lease.Project = original.Worktree.Location.ProjectPath()
	original.Lease.Path = original.Worktree.Location.PoolRoot() + "/slot/repo"
	for _, mode := range []sshcommand.Authentication{"", sshcommand.ForwardedAgent} {
		request := original
		request.Worktree.Location.Authentication, request.Lease.Remote.Authentication = mode, mode
		location := request.Worktree.Location
		profile := herdr.SavedMachine{ID: location.Machine, Target: location.Target, Enabled: true}
		runner := &worktreeAuthenticationRunner{t: t, mode: mode}
		transport := WorktreeTransport{Runner: runner}
		_, _ = transport.Acquire(t.Context(), profile, request.Worktree)
		_, _ = transport.Inspect(t.Context(), profile, request.Worktree)
		_ = transport.ImportBase(
			t.Context(),
			profile,
			request.Worktree,
			strings.NewReader("bundle"),
		)
		_, _ = transport.Return(t.Context(), profile, request)
		_, _ = transport.PrepareLaunchSite(
			t.Context(),
			profile,
			LaunchSiteRequest{
				Worktree:   request.Worktree,
				Lease:      request.Lease,
				Generation: "gen-1",
			},
		)
		_, _ = transport.ExportResult(
			t.Context(),
			profile,
			WorktreeResultRequest{Worktree: request.Worktree, Lease: request.Lease},
			io.Discard,
		)
		if runner.calls != 6 {
			t.Fatalf("operation skipped its captured authentication: calls=%d", runner.calls)
		}
	}
}
