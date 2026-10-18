package machineprep

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestMachineAuthenticationIsExplicitAndValidated(t *testing.T) {
	for _, mode := range []sshcommand.Authentication{"", sshcommand.RemoteCredentials, sshcommand.ForwardedAgent, "unknown"} {
		configuration := Configuration{
			Target:         testMachine().Target,
			Base:           "/remote/base",
			Agent:          "codex",
			Authentication: mode,
			Repositories: []Repository{
				{Name: "concierge", URL: "git@example.org:owner/concierge.git"},
			},
		}
		data, err := json.Marshal(map[string]Configuration{testMachine().ID: configuration})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeConfiguration(data, testMachine())
		if mode == "unknown" {
			if err == nil {
				t.Fatal("unknown authentication mode accepted")
			}
			continue
		}
		if err != nil || decoded.Authentication != mode {
			t.Fatalf("mode=%q decoded=%+v err=%v", mode, decoded, err)
		}
	}
}

type forwardingPreparation struct{ t *testing.T }

func (runner forwardingPreparation) Run(
	_ context.Context,
	arguments []string,
	_ io.Reader,
) ([]byte, []byte, error) {
	if !slices.Contains(arguments, "ForwardAgent=yes") ||
		slices.Contains(arguments, "ForwardAgent=no") {
		runner.t.Fatalf("configured preparation did not forward the agent: %v", arguments)
	}
	if strings.Contains(arguments[len(arguments)-1], "concierge-client-build") {
		return []byte("status: client-ready\n"), nil, nil
	}
	return []byte("status: repositories-ready\n"), nil, nil
}

func TestPreparationUsesConfiguredForwardingMode(t *testing.T) {
	configuration := Configuration{
		Target:         testMachine().Target,
		Base:           "/remote/base",
		Agent:          "codex",
		Authentication: sshcommand.ForwardedAgent,
		Repositories: []Repository{
			{Name: "concierge", URL: "git@example.org:owner/concierge.git"},
		},
	}
	preparer := Preparer{Runner: forwardingPreparation{t}}
	if err := preparer.PrepareRepositories(t.Context(), testMachine(), configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.BuildClient(t.Context(), testMachine(), configuration, strings.Repeat("a", 40), strings.NewReader("bundle")); err != nil {
		t.Fatal(err)
	}
}

func TestMissingForwardedAgentStopsBeforeRemoteChecks(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	runner := &probeRunner{t: t}
	report, err := (Checker{Runner: runner, Authentication: sshcommand.ForwardedAgent}).Check(
		context.Background(), testMachine(), "codex", nil)
	if err != nil || report.Ready() || len(runner.commands) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(report.Checks) != 1 || report.Checks[0].Name != "primary-agent" ||
		!strings.Contains(report.Checks[0].Step, "ssh-add") {
		t.Fatalf("missing primary recovery step: %+v", report)
	}
}
