package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReturnRemovesOnlyIgnoredStoreLinks(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			kind    string
			target  string
			ignored bool
			removed bool
		}{
			{"store link", "link", "/nix/store/missing-config", true, true},
			{"store directory link", "link", "/nix/store/missing-directory/", true, true},
			{"regular file", "file", "", true, false},
			{"directory", "directory", "", true, false},
			{"untracked file", "file", "", false, false},
			{"untracked store link", "link", "/nix/store/missing-config", false, false},
			{"other link", "link", "/tmp/config", true, false},
			{"store prefix", "link", "/nix/store-other/config", true, false},
			{"escaped store", "link", "/nix/store/../config", true, false},
			{"tracked change", "tracked", "", false, false},
		} {
			t.Run(fmt.Sprintf("detached=%t/%s", detached, test.name), func(t *testing.T) {
				client, specification := checkoutForTest(t)
				if detached {
					gitForTest(
						t,
						specification.Project,
						"worktree",
						"add",
						"--detach",
						specification.Path,
						specification.BaseCommit,
					)
				} else if err := client.Prepare(context.Background(), specification); err != nil {
					t.Fatal(err)
				}
				name := "generated config\n.yaml"
				if test.kind == "tracked" {
					name = "file.txt"
				}
				path := filepath.Join(specification.Path, name)
				if test.ignored {
					if err := os.WriteFile(filepath.Join(specification.Project, ".git", "info", "exclude"), []byte("generated*\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				switch test.kind {
				case "link":
					err = os.Symlink(test.target, path)
				case "directory":
					err = os.Mkdir(path, 0700)
				default:
					err = os.WriteFile(path, []byte("pending\n"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				var removed []string
				client.RemovedStoreLink = func(path string) { removed = append(removed, path) }
				verify := client.VerifyReturn
				if detached {
					verify = client.VerifyUnusedLeaseReturn
				}
				err = verify(context.Background(), specification)
				if (err == nil) != test.removed {
					t.Fatalf("return verification: %v", err)
				}
				_, err = os.Lstat(path)
				if os.IsNotExist(err) != test.removed {
					t.Fatalf("entry preservation: %v", err)
				}
				if test.removed && !reflect.DeepEqual(removed, []string{path}) {
					t.Fatalf("removed links: %q", removed)
				}
				if !test.removed && len(removed) != 0 {
					t.Fatalf("reported preserved entry: %q", removed)
				}
			})
		}
	}
}

func TestUnusedLeaseReturnPreservesUnsafeDetachedWork(t *testing.T) {
	for _, condition := range []string{"clean reachable", "orphan commit", "dirty file", "ignored file", "different branch"} {
		t.Run(condition, func(t *testing.T) {
			client, specification := checkoutForTest(t)
			gitForTest(
				t,
				specification.Project,
				"worktree",
				"add",
				"--detach",
				specification.Path,
				specification.BaseCommit,
			)
			switch condition {
			case "orphan commit":
				gitForTest(t, specification.Path, "switch", "--orphan", "orphan-result")
				gitForTest(
					t,
					specification.Path,
					"-c",
					"user.name=Test",
					"-c",
					"user.email=test@example.invalid",
					"commit",
					"--allow-empty",
					"-m",
					"Detached work",
				)
				gitForTest(t, specification.Path, "switch", "--detach")
				gitForTest(t, specification.Project, "branch", "-D", "orphan-result")
			case "dirty file":
				if err := os.WriteFile(filepath.Join(specification.Path, "file.txt"), []byte("pending work\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ignored file":
				if err := os.WriteFile(filepath.Join(specification.Project, ".git", "info", "exclude"), []byte("private.txt\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(specification.Path, "private.txt"), []byte("pending ignored work\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "different branch":
				gitForTest(t, specification.Path, "switch", "-c", "other-work")
			}
			before := gitForTest(t, specification.Path, "rev-parse", "HEAD")
			err := client.VerifyUnusedLeaseReturn(context.Background(), specification)
			if (err == nil) != (condition == "clean reachable") {
				t.Fatalf("return verification: %v", err)
			}
			if after := gitForTest(t, specification.Path, "rev-parse", "HEAD"); after != before {
				t.Fatal("verification changed HEAD")
			}
		})
	}
}
