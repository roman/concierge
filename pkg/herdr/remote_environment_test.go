package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteWorktreeEnvironmentStartsWithoutPrimaryFilesystemProbes(t *testing.T) {
	for _, environment := range []WorktreeEnvironment{PlainWorktree, DirenvWorktree, NixWorktree} {
		t.Run(string(environment), func(t *testing.T) {
			responses := []runResponse{
				{stderr: []byte(`{"error":{"code":"agent_not_found"}}`), err: errors.New("absent")},
			}
			if environment != PlainWorktree {
				responses = append(responses, runResponse{}, runResponse{})
			}
			responses = append(
				responses,
				runResponse{},
				runResponse{
					stdout: []byte(
						`{"result":{"agent":{"agent":"codex","agent_status":"idle","pane_id":"p1"}}}`,
					),
				},
			)
			runner := &fakeRunner{responses: responses}
			client := Client{Runner: runner, machine: "remote-machine"}
			path := "/remote-only/worker's checkout"
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("fixture path exists on the primary")
			}
			if err := client.StartRemoteInWorktree(context.Background(), "p1", "worker", "codex", path, []string{"resume", "native-id"}, environment); err != nil {
				t.Fatal(err)
			}
			joined := ""
			for _, call := range runner.calls {
				joined += strings.Join(call, " ") + "\n"
			}
			if environment == DirenvWorktree && !strings.Contains(joined, "'direnv' 'exec'") {
				t.Fatalf("missing remote direnv wrapper: %s", joined)
			}
			if environment == NixWorktree && !strings.Contains(joined, "'nix' 'develop'") {
				t.Fatalf("missing remote Nix wrapper: %s", joined)
			}
			if environment == NixWorktree &&
				!strings.Contains(joined, "'--extra-experimental-features' 'nix-command flakes'") {
				t.Fatal("remote Nix startup depends on global configuration")
			}
			if environment == PlainWorktree && strings.Contains(joined, "concierge-environment-") {
				t.Fatal("injected a shell environment into a plain worktree")
			}
		})
	}
}

func TestRemoteEnvironmentInspectionEnablesFlakesForItsCommand(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "flake.nix"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case " $* " in
*' --extra-experimental-features nix-command flakes '*) printf true ;;
*) exit 72 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	environment, err := DetectWorktreeEnvironment(context.Background(), worktree)
	if err != nil || environment != NixWorktree {
		t.Fatalf("remote flakes inspection: %s %v", environment, err)
	}
}

func TestRemoteWorktreeEnvironmentRejectsUnknownModesAndLocalClients(t *testing.T) {
	for _, client := range []Client{{}, {machine: "remote-machine"}} {
		if err := client.StartRemoteInWorktree(context.Background(), "p1", "worker", "codex", "/remote/worker", nil, WorktreeEnvironment("unknown")); err == nil {
			t.Fatal("accepted an unverified remote environment")
		}
	}
	if err := (Client{}).StartRemoteInWorktree(context.Background(), "p1", "worker", "codex", "/remote/worker", nil, PlainWorktree); err == nil {
		t.Fatal("used a local client for remote startup")
	}
}
