package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type launchSiteSSHFixture struct {
	t        *testing.T
	request  LaunchSiteRequest
	response LaunchSite
	calls    int
}

type failedLaunchSSH struct {
	output []byte
	stderr []byte
	err    error
}

func (runner failedLaunchSSH) Run(context.Context, []string, io.Reader) ([]byte, []byte, error) {
	return runner.output, runner.stderr, runner.err
}

func TestRemoteLaunchSiteTransportReportsFailureStage(t *testing.T) {
	_, request := launchSiteFixture(t)
	profile := herdr.SavedMachine{ID: request.Worktree.Location.Machine,
		Target: request.Worktree.Location.Target, Enabled: true}
	for _, test := range []struct {
		name   string
		runner failedLaunchSSH
		want   string
	}{
		{"transport", failedLaunchSSH{err: errors.New("exit status 255")}, "transport"},
		{"timeout", failedLaunchSSH{err: context.DeadlineExceeded}, "timeout"},
		{"stderr", failedLaunchSSH{stderr: []byte("remote warning")}, "stderr"},
		{"decode", failedLaunchSSH{output: []byte("invalid JSON")}, "response decoding"},
		{"validation", failedLaunchSSH{output: []byte(`{}`)}, "response validation"},
		{"environment", failedLaunchSSH{output: []byte(`{"launch_failure":"environment inspection","diagnostics":"archive HTTP 404"}`), err: errors.New("exit status 1")}, "environment inspection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var diagnostics bytes.Buffer
			_, err := (WorktreeTransport{Runner: test.runner, Diagnostics: &diagnostics}).PrepareLaunchSite(
				t.Context(),
				profile,
				request,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) ||
				errors.Is(err, ErrLaunchDirectoryOwnership) {
				t.Fatalf("stage %s: %v", test.want, err)
			}
			if test.name == "environment" &&
				!strings.Contains(diagnostics.String(), "archive HTTP 404") {
				t.Fatalf("lost remote diagnostics: %s", &diagnostics)
			}
		})
	}
}

func (fixture *launchSiteSSHFixture) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	fixture.calls++
	if !strings.Contains(arguments[len(arguments)-1], "'worker-worktree' 'prepare-launch'") {
		fixture.t.Fatal("used an unfixed setup command")
	}
	request, err := DecodeLaunchSiteRequest(input)
	if err != nil || request != fixture.request {
		fixture.t.Fatal("changed the prepared launch identity")
	}
	data, err := json.Marshal(fixture.response)
	return data, nil, err
}

func TestRemoteLaunchSiteTransportPinsLeaseAndPrivatePaths(t *testing.T) {
	manager, request := launchSiteFixture(t)
	site, err := manager.PrepareLaunchSite(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &launchSiteSSHFixture{t: t, request: request, response: site}
	profile := herdr.SavedMachine{
		ID:      request.Worktree.Location.Machine,
		Target:  request.Worktree.Location.Target,
		Enabled: true,
	}
	transport := WorktreeTransport{Runner: fixture}
	got, err := transport.PrepareLaunchSite(context.Background(), profile, request)
	if err != nil || got != site {
		t.Fatalf("confirmed site: %+v %v", got, err)
	}
	fixture.response.Tools.TaskDirectory = "/foreign/task"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("accepted caller-selected paths")
	}
	fixture.response = site
	fixture.response.Environment = "unknown"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("accepted an unknown environment")
	}
	profile.Target = "changed-target"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("followed a changed target")
	}
	if fixture.calls != 3 {
		t.Fatal("contacted the changed target")
	}
}
