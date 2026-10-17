package machineprep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPreparedClientRequiresOwnedMetadataAndUsableBinary(t *testing.T) {
	for _, kind := range []string{"ready", "no metadata", "foreign owner", "invalid commit", "missing executable", "symbolic metadata"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			commit := strings.Repeat("a", 40)
			owner := "machine-id\n"
			if kind == "foreign owner" {
				owner = "other-machine\n"
			}
			if err := os.WriteFile(filepath.Join(base, "machine-owner"), []byte(owner), 0600); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(base, "client-commit")
			if kind != "no metadata" {
				value := commit + "\n"
				if kind == "invalid commit" {
					value = "../../outside\n"
				}
				if kind == "symbolic metadata" {
					external := filepath.Join(t.TempDir(), "commit")
					if err := os.WriteFile(external, []byte(value), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(external, metadata); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(metadata, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			executable := filepath.Join(base, "clients", commit, "bin", "concierge")
			if kind != "missing executable" {
				if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			configuration := Configuration{
				Target: "baker",
				Base:   base,
				Agent:  "codex",
				Repositories: []Repository{
					{Name: "concierge", URL: "ssh://git.test/concierge.git"},
				},
			}
			client, err := (Preparer{Runner: localPreparation{}}).ReadClient(
				context.Background(),
				testMachine(),
				configuration,
			)
			if kind == "ready" {
				if err != nil || client.Commit != commit || client.Executable != executable {
					t.Fatalf("client=%+v error=%v", client, err)
				}
			} else if err == nil {
				t.Fatal("reported an unprepared or foreign client ready")
			}
		})
	}
}
