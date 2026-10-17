package herdr

import (
	"context"
	"errors"
	"os"
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
			if environment == PlainWorktree && strings.Contains(joined, "concierge-environment-") {
				t.Fatal("injected a shell environment into a plain worktree")
			}
		})
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
