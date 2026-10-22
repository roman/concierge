package versioncheck

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

const statusFixture = `client:
  version: 0.9.5
  protocol: 22
server:
  status: running
  version: 0.9.0
  endpoint_compatible: yes
  private_protocol_compatible: no
update:
  server_binary_stale: yes
`

func TestParseHerdrStatus(t *testing.T) {
	for _, test := range []struct {
		name, data string
		valid      bool
	}{
		{"running", statusFixture, true},
		{"unknown version", strings.Replace(statusFixture, "  version: 0.9.0\n", "", 1), false},
		{"duplicate version", statusFixture + "server:\n  version: 9\n", false},
		{"invalid flag", strings.Replace(statusFixture, "stale: yes", "stale: maybe", 1), false},
		{"old format", "herdr 0.9.0", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := parseHerdrStatus([]byte(test.data))
			if (err == nil) != test.valid {
				t.Fatalf("snapshot=%+v err=%v", snapshot, err)
			}
			if test.valid &&
				(snapshot.HerdrClient != "0.9.5" || snapshot.HerdrServer != "0.9.0" || snapshot.Stale != "yes" || snapshot.PrivateCompatible != "no") {
				t.Fatal(snapshot)
			}
		})
	}
}

type probeSSH struct{ commands []string }

func (runner *probeSSH) Run(
	_ context.Context,
	arguments []string,
	_ io.Reader,
) ([]byte, []byte, error) {
	if arguments[0] == "-G" {
		return []byte("hostname baker\nforwardagent no\nforwardx11 no\ntunnel false\n"), nil, nil
	}
	command := arguments[len(arguments)-1]
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, "'herdr' 'status'") {
		return []byte(statusFixture), nil, nil
	}
	if strings.Contains(command, "'codex' '--version'") {
		return []byte("codex-cli 1.0\n"), nil, nil
	}
	return nil, nil, errors.New("unexpected command")
}

func TestCollectorUsesSelectedToolsAndTrustedSSH(t *testing.T) {
	runner := &probeSSH{}
	var commands []string
	collector := Collector{
		Herdr:  "/tools/herdr",
		Remote: runner,
		Local: func(_ context.Context, executable string, args ...string) ([]byte, error) {
			commands = append(commands, executable+" "+strings.Join(args, " "))
			if executable == "/tools/herdr" {
				return []byte(statusFixture), nil
			}
			return []byte("codex-cli 2.0\n"), nil
		},
	}
	report := collector.Inspect(
		t.Context(),
		Options{
			PrimaryCommit: "abc",
			RemoteCommit:  "def",
			Agent:         "codex",
			Machine: herdr.SavedMachine{
				ID:      "baker",
				Label:   "baker",
				Target:  "baker",
				Enabled: true,
			},
			Authentication: sshcommand.RemoteCredentials,
		},
	)
	if len(commands) != 2 || commands[0] != "/tools/herdr status" ||
		commands[1] != "codex --version" ||
		len(runner.commands) != 2 {
		t.Fatalf("local=%v remote=%v", commands, runner.commands)
	}
	if !report.Blocked() {
		t.Fatal("lost explicit protocol incompatibility")
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "update") || strings.Contains(command, "stop") {
			t.Fatal(command)
		}
	}
}

func TestUnavailableProbeWarnsWithoutBlocking(t *testing.T) {
	collector := Collector{
		Local: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") },
	}
	report := collector.Inspect(t.Context(), Options{Agent: "claude"})
	if report.Blocked() || len(report.Warnings) == 0 {
		t.Fatal(report)
	}
}
