package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteCleanupNeedsTrustedLandedOrScoutProof(t *testing.T) {
	for _, outcome := range []WorktreeReturnOutcome{WorktreeLanded, WorktreeScout, WorktreeUnchanged, WorktreeAbandoned} {
		t.Run(string(outcome), func(t *testing.T) {
			for _, busy := range []bool{false, true} {
				manager, request := remoteReturnFixture(t)
				request.Proof.Outcome = outcome
				common := remoteProjectGit(
					t,
					manager.Git.Executable,
					request.Lease.Path,
					"rev-parse",
					"--path-format=absolute",
					"--git-common-dir",
				)
				if err := os.WriteFile(filepath.Join(common, "info", "exclude"), []byte("ignored\n"), 0600); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"ignored", "untracked"} {
					if err := os.WriteFile(filepath.Join(request.Lease.Path, name), []byte("output"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if busy {
					manager.CheckQuiet = func(context.Context, string) error { return ErrWorktreeBusy }
				}
				var removed []string
				manager.Git.RemovedPath = func(path string) { removed = append(removed, path) }
				_, err := manager.Return(t.Context(), request)
				wantClean := !busy && (outcome == WorktreeLanded || outcome == WorktreeScout)
				if (err == nil) != wantClean {
					t.Fatalf("busy=%t: %v", busy, err)
				}
				if wantClean {
					if len(removed) != 2 {
						t.Fatalf("removed paths: %q", removed)
					}
					removed = nil
					if _, err := manager.Return(t.Context(), request); err != nil {
						t.Fatal(err)
					}
					if len(removed) != 2 {
						t.Fatalf("retry lost removal diagnostics: %q", removed)
					}
					continue
				}
				if len(removed) != 0 {
					t.Fatalf("blocked cleanup removed %q", removed)
				}
				for _, name := range []string{"ignored", "untracked"} {
					if _, err := os.Stat(filepath.Join(request.Lease.Path, name)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := manager.Inspect(t.Context(), request.Worktree); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
