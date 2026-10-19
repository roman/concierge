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
	outputs  map[string]string
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
	for fragment, output := range runner.outputs {
		if strings.Contains(command, fragment) {
			return []byte(output), nil, nil
		}
	}
	for fragment, output := range map[string]string{
		"'true'": "", "'uname' '-s'": "Darwin\n", "'uname' '-m'": "arm64\n",
		"'nix' '--version'": "nix (Nix) 2.31.2\n", "'nix' 'eval'": "\"aarch64-darwin\"\n",
		"'herdr' '--version'": "herdr 0.9.5\n", "'ls-remote'": "", "'login' 'status'": "Logged in\n",
		"'auth' 'status'": "{}\n", "'command' '-v' 'lsof'": "/usr/bin/lsof\n",
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
			checks := make(map[string]Check)
			for _, check := range report.Checks {
				checks[check.Name] = check
			}
			for _, name := range []string{"ssh", "ssh-policy", "platform", "nix", "flakes", "herdr", "repository:project", "agent:" + agent} {
				if checks[name].Status != "ready" {
					t.Fatalf("missing ready check %s: %+v", name, report)
				}
			}
			if checks["platform"].Detail != "Darwin arm64" {
				t.Fatalf("wrong platform: %+v", checks["platform"])
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

func TestCheckRequiresSupportedHerdrAndCleanupTool(t *testing.T) {
	for _, test := range []struct {
		name, version, missing, status string
	}{
		{"minimum", "herdr 0.9.5", "", "ready"},
		{"newer", "herdr 1.0.0", "", "ready"},
		{"old", "herdr 0.9.4", "", "missing"},
		{"malformed", "herdr latest", "", "missing"},
		{"empty", "", "", "missing"},
		{"prerelease", "herdr 0.9.5-rc1", "", "missing"},
		{"cleanup tool absent", "herdr 0.9.5", "'command' '-v' 'lsof'", "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &probeRunner{
				t:       t,
				fail:    test.missing,
				outputs: map[string]string{"'herdr' '--version'": test.version},
			}
			report, err := (Checker{Runner: runner}).Check(t.Context(), testMachine(), "codex", nil)
			if err != nil {
				t.Fatal(err)
			}
			name := "herdr"
			if test.missing != "" {
				name = "lsof"
			}
			for _, check := range report.Checks {
				if check.Name == name {
					if check.Status != test.status ||
						(test.status == "missing" && check.Step == "") {
						t.Fatalf("invalid readiness: %+v", check)
					}
					return
				}
			}
			t.Fatalf("missing %s check", name)
		})
	}
}

func TestCheckSupportedRemotePlatforms(t *testing.T) {
	for _, test := range []struct {
		name, operatingSystem, architecture, nixSystem string
		status                                         string
	}{
		{"apple silicon", "Darwin", "arm64", "aarch64-darwin", "ready"},
		{"linux amd64", "Linux", "x86_64", "x86_64-linux", "ready"},
		{"linux arm64", "Linux", "aarch64", "aarch64-linux", "ready"},
		{"intel mac", "Darwin", "x86_64", "x86_64-darwin", "missing"},
		{"linux armv7", "Linux", "armv7l", "armv7l-linux", "missing"},
		{"freebsd", "FreeBSD", "amd64", "x86_64-freebsd", "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &probeRunner{t: t, outputs: map[string]string{
				"'uname' '-s'": test.operatingSystem + "\n",
				"'uname' '-m'": test.architecture + "\n",
				"'nix' 'eval'": "\"" + test.nixSystem + "\"\n",
			}}
			report, err := (Checker{Runner: runner}).Check(
				t.Context(), testMachine(), "codex", nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range report.Checks {
				if check.Name != "platform" {
					continue
				}
				if check.Status != test.status ||
					check.Detail != test.operatingSystem+" "+test.architecture {
					t.Fatalf("platform = %+v, want %s", check, test.status)
				}
				if check.Status == "missing" && check.Step == "" {
					t.Fatal("unsupported platform has no operator step")
				}
				return
			}
			t.Fatal("platform check is missing")
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
