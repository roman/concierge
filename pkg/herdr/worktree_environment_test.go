package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
			runner := &fakeRunner{responses: []runResponse{
				{stderr: []byte(`{"error":{"code":"agent_not_found"}}`), err: errors.New("absent")},
				{}, {}, {},
				{
					stdout: []byte(
						`{"result":{"agent":{"agent":"` + harness + `","agent_status":"idle","pane_id":"p1"}}}`,
					),
				},
			}}
			arguments := []string{"resume", "session with ' quotes"}
			if err := (Client{Runner: runner}).StartInWorktree(context.Background(), "p1", "worker", harness, worktree, arguments); err != nil {
				t.Fatal(err)
			}
			start := runner.calls[3]
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
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("wrapper: %s: %v", output, err)
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
