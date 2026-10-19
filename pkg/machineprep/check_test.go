package machineprep

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
)

type probeRunner struct {
	t        *testing.T
	fail     string
	commands []string
}

func (runner *probeRunner) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	runner.t.Helper()
	deadline, found := ctx.Deadline()
	if !found || time.Until(deadline) > 15*time.Second || input != nil {
		runner.t.Fatal("probe must have a deadline and no input")
	}
	if arguments[0] == "-G" {
		return []byte(
			"hostname fake-target\nforwardagent no\nforwardx11 no\ntunnel false\n",
		), nil, nil
	}
	joined := strings.Join(arguments, " ")
	for _, option := range []string{"StrictHostKeyChecking=yes", "UpdateHostKeys=no", "ForwardAgent=no", "ClearAllForwardings=yes", "BatchMode=yes"} {
		if !strings.Contains(joined, option) {
			runner.t.Fatalf("missing %s", option)
		}
	}
	command := arguments[len(arguments)-1]
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, runner.fail) && runner.fail != "" {
		return []byte(
				"credential from partial output",
			), []byte(
				"private diagnostic",
			), errors.New(
				"probe failed",
			)
	}
	for fragment, output := range map[string]string{
		"'true'": "", "'uname' '-s'": "Darwin\n", "'uname' '-m'": "arm64\n",
		"'nix' '--version'": "nix (Nix) 2.31.2\n", "'nix' 'eval'": "\"aarch64-darwin\"\n",
		"'herdr' '--version'": "herdr 0.9.5\n", "'ls-remote'": "", "'login' 'status'": "Logged in\n",
		"'auth' 'status'": "{}\n",
	} {
		if strings.Contains(command, fragment) {
			return []byte(output), nil, nil
		}
	}
	runner.t.Fatalf("unexpected remote command: %s", command)
	return nil, nil, nil
}

func TestCheckReportsPrerequisitesWithoutCredentialsOrWrites(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			runner := &probeRunner{t: t}
			report, err := (Checker{Runner: runner}).Check(
				context.Background(),
				testMachine(),
				agent,
				[]Repository{{Name: "project", URL: "git@example.org:owner/project.git"}},
			)
			if err != nil || !report.Ready() {
				t.Fatalf("report=%+v error=%v", report, err)
			}
			if len(report.Checks) != 8 || report.Checks[2].Detail != "Darwin arm64" {
				t.Fatalf("incomplete report: %+v", report)
			}
			for _, command := range runner.commands {
				for _, forbidden := range []string{"clone", "fetch", "mkdir", "install", "printenv", "cat"} {
					if strings.Contains(command, "'"+forbidden+"'") {
						t.Fatalf("check mutates or reads credentials: %s", command)
					}
				}
			}
		})
	}
}

func TestCheckFailuresReportOperatorStepsAndDiscardDiagnostics(t *testing.T) {
	for _, fragment := range []string{"'true'", "'uname' '-s'", "'nix' '--version'", "'nix' 'eval'", "'herdr'", "'ls-remote'", "'login'"} {
		t.Run(fragment, func(t *testing.T) {
			runner := &probeRunner{t: t, fail: fragment}
			report, err := (Checker{Runner: runner}).Check(
				context.Background(),
				testMachine(),
				"codex",
				[]Repository{{Name: "project", URL: "git@example.org:owner/project.git"}},
			)
			if err != nil || report.Ready() {
				t.Fatalf("report=%+v error=%v", report, err)
			}
			failed := 0
			for _, check := range report.Checks {
				if check.Status == "missing" {
					failed++
					if check.Step == "" {
						t.Fatal("missing operator step")
					}
				}
				if strings.Contains(check.Detail+check.Step, "credential from") ||
					strings.Contains(check.Detail+check.Step, "private diagnostic") {
					t.Fatal("probe diagnostics disclosed")
				}
			}
			if failed != 1 {
				t.Fatalf("expected one prerequisite failure: %+v", report)
			}
			if fragment == "'true'" && len(runner.commands) != 1 {
				t.Fatal("continued after untrusted or unreachable SSH")
			}
		})
	}
}

func TestCheckRejectsInvalidInputsBeforeSSH(t *testing.T) {
	for _, input := range []struct{ agent, url string }{
		{"unknown", "git@example.org:owner/project.git"},
		{"codex", "https://user:password@example.org/project"},
		{"codex", "-upload-pack=evil"},
		{"codex", "/local/repository"},
	} {
		runner := &probeRunner{t: t}
		_, err := (Checker{Runner: runner}).Check(
			context.Background(),
			testMachine(),
			input.agent,
			[]Repository{{Name: "project", URL: input.url}},
		)
		if err == nil || len(runner.commands) != 0 {
			t.Fatal("invalid input reached SSH")
		}
	}
}

func testMachine() herdr.SavedMachine {
	return herdr.SavedMachine{
		ID:      "machine-id",
		Label:   "dev-remote",
		Target:  "dev-remote",
		Session: "default",
		Enabled: true,
	}
}
