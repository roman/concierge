package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscardInventoryPreservesSalvageableWork(t *testing.T) {
	for _, kind := range []string{"clean", "tracked", "untracked", "ignored", "commit", "divergent commit"} {
		t.Run(kind, func(t *testing.T) {
			client, specification := checkoutForTest(t)
			if err := client.Prepare(t.Context(), specification); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "tracked":
				if err := os.WriteFile(filepath.Join(specification.Path, "file.txt"), []byte("unfinished"), 0600); err != nil {
					t.Fatal(err)
				}
			case "untracked", "ignored":
				if kind == "ignored" {
					if err := os.WriteFile(filepath.Join(specification.Project, ".git", "info", "exclude"), []byte("product\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(specification.Path, "product"), []byte("unfinished"), 0600); err != nil {
					t.Fatal(err)
				}
			case "divergent commit":
				tree := strings.TrimSpace(
					gitForTest(t, specification.Path, "rev-parse", "HEAD^{tree}"),
				)
				commit := strings.TrimSpace(
					gitForTest(
						t,
						specification.Path,
						"-c",
						"user.name=Test",
						"-c",
						"user.email=test@example.com",
						"commit-tree",
						tree,
						"-m",
						"Unfinished product",
					),
				)
				gitForTest(t, specification.Path, "reset", "--hard", commit)
			case "commit":
				gitForTest(
					t,
					specification.Path,
					"-c",
					"user.name=Test",
					"-c",
					"user.email=test@example.com",
					"commit",
					"--allow-empty",
					"-m",
					"Unfinished product",
				)
			}
			inventory, err := client.InspectDiscard(t.Context(), specification)
			if err != nil {
				t.Fatal(err)
			}
			if inventory.RequiresDecision() != (kind != "clean") {
				t.Fatalf("inventory: %+v", inventory)
			}
			if kind == "ignored" && !strings.Contains(inventory.Files, "!! product") {
				t.Fatalf("ignored file absent: %+v", inventory)
			}
			if kind == "commit" && !strings.Contains(inventory.Commits, "Unfinished product") {
				t.Fatalf("commit absent: %+v", inventory)
			}
			if kind == "untracked" || kind == "ignored" {
				if _, err := os.Stat(filepath.Join(specification.Path, "product")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
