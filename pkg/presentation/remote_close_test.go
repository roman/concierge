package presentation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type remotePaneRunner struct {
	api   *closeRunner
	calls int
}

func (runner *remotePaneRunner) RunMachine(
	ctx context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	runner.calls++
	if machine != "machine-1" || len(args) == 0 || args[0] == "session" || args[0] == "terminal" {
		return nil, nil, errors.New("wrong machine or unsupported forwarded command")
	}
	for _, argument := range args {
		if argument == "--session" {
			return nil, nil, errors.New("forwarded command selected a local session")
		}
	}
	return runner.api.RunSession(ctx, "work", args...)
}

type presentationRemoteChecks struct {
	api       *closeRunner
	failCheck herdr.RemoteCheck
	failure   error
	malformed herdr.RemoteCheck
	checks    []herdr.RemoteCheck
}

func (checks *presentationRemoteChecks) RunCheck(
	ctx context.Context,
	machine, session string,
	check herdr.RemoteCheck,
) ([]byte, []byte, error) {
	checks.checks = append(checks.checks, check)
	if machine != "machine-1" || session != "work" {
		return nil, nil, errors.New("wrong remote check identity")
	}
	if check == checks.failCheck {
		return nil, nil, checks.failure
	}
	if check == checks.malformed {
		return []byte("invalid"), nil, nil
	}
	if check == herdr.SessionInventory {
		return checks.api.RunSession(ctx, session, "session", "list", "--json")
	}
	return checks.api.RunSession(ctx, session, "terminal", "title", "clear")
}

func TestRemoteClosePreservesViewerChecks(t *testing.T) {
	for _, test := range []struct {
		name, viewer string
		failCheck    herdr.RemoteCheck
		failure      error
		malformed    herdr.RemoteCheck
		want         error
	}{
		{name: "no viewer", viewer: "no_foreground_client"},
		{name: "viewer present", viewer: "cleared", want: ErrFocusUnsafe},
		{name: "SSH viewer failure", viewer: "no_foreground_client", failCheck: herdr.ForegroundViewer, failure: herdr.ErrUnreachable, want: herdr.ErrUnreachable},
		{name: "SSH inventory timeout", failCheck: herdr.SessionInventory, failure: herdr.ErrUnreachable, want: herdr.ErrUnreachable},
		{name: "malformed viewer", malformed: herdr.ForegroundViewer, want: herdr.ErrResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := newCloseRunner(t)
			api.paneWorkspace, api.paneTab, api.viewer = "ws-primary", "tab-primary", test.viewer
			remote := &remotePaneRunner{api: api}
			checks := &presentationRemoteChecks{
				api:       api,
				failCheck: test.failCheck,
				failure:   test.failure,
				malformed: test.malformed,
			}
			client := herdr.Client{MachineRunner: remote, RemoteChecks: checks}
			target, err := herdr.ParseEndpoint("machine-1", "work", "pane-worker")
			if err != nil {
				t.Fatal(err)
			}
			err = closeBoundPane(
				context.Background(),
				client,
				target,
				"ws-primary",
				"tab-primary",
				func() error { return nil },
				filepath.Join(t.TempDir(), "locks"),
			)
			if !errors.Is(err, test.want) || api.paneExists != (test.want != nil) {
				t.Fatalf("close error=%v exists=%v calls=%v", err, api.paneExists, api.calls)
			}
			if test.failCheck == herdr.SessionInventory && remote.calls != 0 {
				t.Fatalf("pane was contacted after unreachable inventory: %v", api.calls)
			}
		})
	}
}

func TestRemoteCloseRestoresFocus(t *testing.T) {
	api := newCloseRunner(t)
	api.shiftOnClose = true
	remote := &remotePaneRunner{api: api}
	checks := &presentationRemoteChecks{api: api}
	target, err := herdr.ParseEndpoint("machine-1", "work", "pane-worker")
	if err != nil {
		t.Fatal(err)
	}
	err = closeBoundPane(
		context.Background(),
		herdr.Client{MachineRunner: remote, RemoteChecks: checks},
		target,
		"ws-worker",
		"tab-worker",
		func() error { return nil },
		filepath.Join(t.TempDir(), "locks"),
	)
	if err != nil || api.paneExists || api.activeTab != "tab-primary" {
		t.Fatalf(
			"close error=%v exists=%v focus=%s calls=%v",
			err,
			api.paneExists,
			api.activeTab,
			api.calls,
		)
	}
}

func TestRemoteSocketIdentityDoesNotResolvePrimaryPaths(t *testing.T) {
	directory := t.TempDir()
	link := filepath.Join(directory, "remote-path")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	api := newCloseRunner(t)
	api.socket = filepath.Join(link, "work.sock")
	client, err := (herdr.Client{MachineRunner: &remotePaneRunner{api: api}, RemoteChecks: &presentationRemoteChecks{api: api}}).ForEndpoint(
		"machine-1",
		"work",
	)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := sessionSocket(context.Background(), client, "work")
	if err != nil || identity != "machine-1\x00"+api.socket {
		t.Fatalf("socket identity=%q error=%v", identity, err)
	}
}
