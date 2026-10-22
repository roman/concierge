package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestWorktreeEnvironment(t *testing.T) {
	for _, test := range []struct {
		name         string
		files        []string
		defaultShell string
		want         []string
	}{
		{name: "envrc", files: []string{".envrc", "flake.nix"}, want: []string{"direnv", "exec"}},
		{name: "flake", files: []string{"flake.nix"}, defaultShell: "true", want: []string{"nix", "develop"}},
		{name: "flake without default", files: []string{"flake.nix"}, defaultShell: "false"},
		{name: "neither"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worktree := t.TempDir()
			for _, name := range test.files {
				if err := os.WriteFile(filepath.Join(worktree, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\nprintf '%s\\n' '"+test.defaultShell+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			prefix, err := worktreeEnvironment(context.Background(), worktree)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if test.want != nil {
				want = append(append([]string{}, test.want...), worktree)
				if test.want[0] == "nix" {
					want = append(want, "-c")
				}
			}
			if !reflect.DeepEqual(prefix, want) {
				t.Fatalf("prefix = %q, want %q", prefix, want)
			}
		})
	}
}

func TestStartInWorktreeWrapsNativeResume(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			worktree := filepath.Join(t.TempDir(), "worker's checkout")
			if err := os.Mkdir(worktree, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(worktree, ".envrc"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			runner := &environmentWaitRunner{
				outcome: "ready",
				fakeRunner: fakeRunner{responses: []runResponse{
					{
						stderr: []byte(`{"error":{"code":"agent_not_found"}}`),
						err:    errors.New("absent"),
					},
					{}, {}, {},
					{
						stdout: []byte(
							`{"result":{"agent":{"agent":"` + harness + `","agent_status":"idle","pane_id":"p1"}}}`,
						),
					},
				}},
			}
			arguments := []string{"resume", "session with ' quotes"}
			if err := (Client{Runner: runner}).StartInWorktree(context.Background(), "p1", "worker", harness, worktree, arguments); err != nil {
				t.Fatal(err)
			}
			start := runner.calls[3]
			if !strings.Contains(strings.Join(start, " "), "--timeout 300000 --") {
				t.Fatalf("native startup timeout exceeds Herdr's limit: %q", start)
			}
			if !strings.Contains(strings.Join(runner.calls[2], " "), "--timeout 900000") {
				t.Fatalf("devshell build lacks time for evaluation: %q", runner.calls[2])
			}
			if !reflect.DeepEqual(start[len(start)-2:], arguments) {
				t.Fatalf("resume arguments changed: %q", start)
			}
			if runner.calls[2][1] != "wait-output" {
				t.Fatalf("missing environment readiness check: %q", runner.calls)
			}
			bin := t.TempDir()
			capture := filepath.Join(t.TempDir(), "arguments")
			if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			script := runner.calls[1][3] + "\n" + harness + " " + shellQuote(
				arguments[0],
			) + " " + shellQuote(
				arguments[1],
			)
			command := exec.Command("bash", "-c", script)
			command.Env = append(os.Environ(), "PATH="+bin, "CAPTURE="+capture)
			wrapperOutput, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("wrapper: %s: %v", wrapperOutput, err)
			}
			marker := strings.TrimSuffix(
				strings.TrimPrefix(waitRegex(runner.calls[2]), "^"),
				"-(ready|failed)$",
			)
			// An echoed or wrapped command line must never satisfy the wait.
			if strings.Contains(runner.calls[1][3], marker+"-") {
				t.Fatalf(
					"pane command contains a complete readiness marker: %s",
					runner.calls[1][3],
				)
			}
			if !regexp.MustCompile(`(?m)` + waitRegex(runner.calls[2])).Match(wrapperOutput) {
				t.Fatalf("devshell build printed no readiness marker: %s", wrapperOutput)
			}
			output, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Join(
				[]string{"exec", worktree, harness, arguments[0], arguments[1], ""},
				"\n",
			)
			if string(output) != want {
				t.Fatalf("wrapped arguments = %q, want %q", output, want)
			}
		})
	}
}

func TestStartInWorktreeStopsWhenDevshellBuildFails(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".envrc"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	runner := &environmentWaitRunner{
		outcome: "failed",
		fakeRunner: fakeRunner{responses: []runResponse{
			{stderr: []byte(`{"error":{"code":"agent_not_found"}}`), err: errors.New("absent")},
			{}, {},
			{stdout: []byte("$ codex\nerror: flake has no devShell\n")},
		}},
	}
	err := (Client{Runner: runner}).StartInWorktree(
		context.Background(),
		"p1",
		"worker",
		"codex",
		worktree,
		nil,
	)
	if !errors.Is(err, ErrNativeStartup) ||
		!strings.Contains(err.Error(), "error: flake has no devShell") {
		t.Fatalf("error = %v, want native startup failure with the Nix diagnostic", err)
	}
	for _, call := range runner.calls {
		if call[0] == "agent" && call[1] == "start" {
			t.Fatalf("started the agent after a failed devshell build: %q", runner.calls)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "-c", runner.calls[1][3])
	command.Env = append(os.Environ(), "PATH="+bin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper: %s: %v", output, err)
	}
	failed := strings.Replace(waitRegex(runner.calls[2]), "(ready|failed)", "failed", 1)
	if !regexp.MustCompile(`(?m)` + failed).Match(output) {
		t.Fatalf("failed devshell build printed no failure marker: %s", output)
	}
}

// environmentWaitRunner answers each environment wait with the marker line
// that the pane command prints for the configured outcome.
type environmentWaitRunner struct {
	fakeRunner
	outcome string
}

func (r *environmentWaitRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	stdout, stderr, err := r.fakeRunner.Run(ctx, args...)
	if len(args) > 1 && args[0] == "pane" && args[1] == "wait-output" {
		marker := strings.TrimSuffix(strings.TrimPrefix(waitRegex(args), "^"), "-(ready|failed)$")
		stdout = []byte(`{"result":{"matched_line":"` + marker + "-" + r.outcome + `"}}`)
	}
	return stdout, stderr, err
}

func waitRegex(args []string) string {
	for index, argument := range args[:len(args)-1] {
		if argument == "--regex" {
			return args[index+1]
		}
	}
	return ""
}
