package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNixDevelopArgumentsReachLocalAndRemoteStartup(t *testing.T) {
	for _, location := range []string{"local", "remote"} {
		t.Run(location, func(t *testing.T) {
			worktree := t.TempDir()
			if err := os.WriteFile(filepath.Join(worktree, "flake.nix"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\nprintf true\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
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
							`{"result":{"agent":{"agent":"codex","agent_status":"idle","pane_id":"p1"}}}`,
						),
					},
				}},
			}
			client := Client{
				Runner: runner,
				NixDevelopArgs: []string{
					"--impure",
					"--option",
					"test-option",
					"value with spaces",
				},
			}
			var err error
			if location == "remote" {
				client.machine = "remote-machine"
				err = client.StartRemoteInWorktree(
					context.Background(),
					"p1",
					"worker",
					"codex",
					worktree,
					nil,
					NixWorktree,
				)
			} else {
				err = client.StartInWorktree(context.Background(), "p1", "worker", "codex", worktree, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			command := runner.calls[1][3]
			// The devshell build and the native wrapper must share one cache entry.
			if strings.Count(
				command,
				"'--impure' '--option' 'test-option' 'value with spaces' '-c'",
			) != 2 {
				t.Fatalf("Nix arguments missing from the build or the wrapper: %s", command)
			}
		})
	}
}
