package remoteworker

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestTrustedTunnelForwardsAgentOnlyWhenSelected(t *testing.T) {
	root, spec := tunnelFixture(t)
	primaryAgent := filepath.Join(root, "primary-agent.sock")
	t.Setenv("SSH_AUTH_SOCK", primaryAgent)
	for _, mode := range []sshcommand.Authentication{"", sshcommand.RemoteCredentials, sshcommand.ForwardedAgent} {
		spec.Authentication = mode
		arguments, err := TunnelArguments(spec, filepath.Join(root, "worker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(spec.Tools.TaskDirectory, "agent.sock") + ":" + primaryAgent
		if slices.Contains(arguments, want) != (mode == sshcommand.ForwardedAgent) {
			t.Fatalf("wrong agent forwarding for mode %q: %v", mode, arguments)
		}
		if !slices.Contains(
			arguments,
			filepath.Join(
				spec.Tools.TaskDirectory,
				"worker.sock",
			)+":"+filepath.Join(
				root,
				"worker.sock",
			),
		) {
			t.Fatal("agent forwarding replaced the worker socket")
		}
	}
}

func TestMissingPrimaryAgentPreventsTunnelProcessStart(t *testing.T) {
	root, spec := tunnelFixture(t)
	spec.Authentication = sshcommand.ForwardedAgent
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(root, "absent-agent.sock"))
	_, err := (SSHProcesses{Executable: "/must-not-start"}).Start(
		t.Context(),
		spec,
		filepath.Join(root, "worker.sock"),
	)
	if !errors.Is(err, sshcommand.ErrPrimaryAgent) || !errors.Is(err, ErrTunnelNotStarted) {
		t.Fatalf("missing agent lost pre-spawn recovery evidence: %v", err)
	}
}

func TestAgentTunnelRefusesMissingOrAmbiguousPrimarySocket(t *testing.T) {
	root, spec := tunnelFixture(t)
	spec.Authentication = sshcommand.ForwardedAgent
	for _, socket := range []string{"", "relative", "/tmp/ambiguous:socket"} {
		t.Setenv("SSH_AUTH_SOCK", socket)
		if _, err := TunnelArguments(spec, filepath.Join(root, "worker.sock")); err == nil {
			t.Fatalf("invalid primary socket accepted: %q", socket)
		}
	}
}
