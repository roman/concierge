package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReturnIgnoredPathValidation(t *testing.T) {
	for _, test := range []struct {
		path  string
		valid bool
	}{
		{".claude/rules/", true}, {".locker/", true}, {".codex/config.toml", true},
		{"generated file", true}, {"", false}, {"/tmp/generated", false},
		{".", false}, {"./", false}, {"../file", false}, {"a/../file", false},
		{".git", false}, {".git/", false}, {"a/.git/config", false},
		{"*", false}, {"a/**", false}, {"a//b", false}, {"./file", false},
		{"a\\b", false}, {"a\n", false}, {"a\x00", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if err := ValidateReturnIgnoredPaths([]string{test.path}); (err == nil) != test.valid {
				t.Fatalf("valid=%t: %v", test.valid, err)
			}
		})
	}
}

func TestReturnIgnoredPathMatching(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{".claude/rules/locker/a.md", true}, {".claude/rules/a.md", true},
		{".claude/rules-other/a.md", false}, {".claude/rules", false},
		{".codex/config.toml", true}, {".codex/config.toml.bak", false},
		{".codex/config.toml/child", false}, {"other", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if got := matchesReturnIgnoredPath(test.path, []string{".claude/rules/", ".codex/config.toml"}); got != test.want {
				t.Fatalf("match=%t, want %t", got, test.want)
			}
		})
	}
}

func TestReturnIgnoredAllowlist(t *testing.T) {
	for _, mode := range []string{"allowed", "unmatched", "dirty", "staged", "untracked", "missing allowlist", "tracked in directory", "external symlink"} {
		t.Run(mode, func(t *testing.T) {
			client, specification := checkoutForTest(t)
			ctx := context.Background()
			if err := client.Prepare(ctx, specification); err != nil {
				t.Fatal(err)
			}
			client.ReturnIgnoredPaths = []string{"generated/"}
			if mode == "missing allowlist" {
				client.ReturnIgnoredPaths = nil
			}
			if err := os.WriteFile(filepath.Join(specification.Project, ".git", "info", "exclude"), []byte("generated/\nunknown.txt\n"), 0600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(specification.Path, "generated")
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			generated := filepath.Join(directory, "config")
			if err := os.WriteFile(generated, []byte("generated"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unmatched":
				if err := os.WriteFile(filepath.Join(specification.Path, "unknown.txt"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dirty", "staged":
				if err := os.WriteFile(filepath.Join(specification.Path, "file.txt"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "staged" {
					gitForTest(t, specification.Path, "add", "file.txt")
				}
			case "untracked":
				if err := os.WriteFile(filepath.Join(specification.Path, "private.txt"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "tracked in directory":
				gitForTest(t, specification.Path, "add", "-f", generated)
				gitForTest(
					t,
					specification.Path,
					"-c",
					"user.name=Test",
					"-c",
					"user.email=test@example.invalid",
					"commit",
					"-m",
					"Track config",
				)
				if err := os.WriteFile(filepath.Join(directory, "ignored"), []byte("generated"), 0600); err != nil {
					t.Fatal(err)
				}
			case "external symlink":
				external := t.TempDir()
				if err := os.WriteFile(filepath.Join(external, "private"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(directory, "link")); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := os.Stat(filepath.Join(external, "private")); err != nil {
						t.Error(err)
					}
				})
			}
			err := client.VerifyReturn(ctx, specification)
			wantClean := mode == "allowed" || mode == "tracked in directory" ||
				mode == "external symlink"
			if (err == nil) != wantClean {
				t.Fatalf("return: %v", err)
			}
			if mode == "unmatched" && !strings.Contains(err.Error(), "unknown.txt") {
				t.Fatalf("missing unmatched path: %v", err)
			}
			if mode == "tracked in directory" {
				if _, err := os.Stat(generated); err != nil {
					t.Fatal(err)
				}
			} else if wantClean {
				if _, err := os.Stat(directory); !os.IsNotExist(err) {
					t.Fatalf("generated directory remains: %v", err)
				}
			}
		})
	}
}
