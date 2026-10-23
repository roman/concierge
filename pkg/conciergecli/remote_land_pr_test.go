package conciergecli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemotePullRequestDeliveryCreatesAndPushesFromPrimary(t *testing.T) {
	for _, mode := range []string{"create", "lost create", "conflicting branch"} {
		t.Run(mode, func(t *testing.T) {
			fixture := remoteLandingFixture(t)
			invoke(t, fixture.root, "", "project", "set", "repo", "--delivery", "pr")
			directory := t.TempDir()
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("REMOTE_LAND_REAL_GIT", git)
			t.Setenv("REMOTE_LAND_STATE", directory)
			t.Setenv("REMOTE_LAND_HEAD", fixture.head)
			t.Setenv("REMOTE_LAND_PROJECT", fixture.project)
			base := landingGit(t, fixture.remote, "rev-parse", "trunk")
			merged := landingGit(
				t,
				fixture.project,
				"commit-tree",
				fixture.head+"^{tree}",
				"-p",
				base,
				"-m",
				"Squash result",
			)
			t.Setenv("REMOTE_LAND_MERGED", merged)
			if mode == "conflicting branch" {
				landingGit(t, fixture.project, "push", "origin", base+":refs/heads/task/result")
			}
			if mode == "lost create" {
				if err := os.WriteFile(filepath.Join(directory, "lose-create"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			transport := `#!/bin/sh
case "$*" in
 *' remote get-url --all origin'|*' remote get-url --push --all origin') printf '%s\n' 'https://github.com/owner/repo.git' ;;
 *) exec "$REMOTE_LAND_REAL_GIT" "$@" ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "git"), []byte(transport), 0700); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$REMOTE_LAND_STATE/calls"
case "$1 $2" in
 'pr list')
  if [ ! -f "$REMOTE_LAND_STATE/created" ]; then printf '[]'; exit; fi
  printf '[{"number":42,"url":"https://github.com/owner/repo/pull/42","headRefOid":"%s","headRefName":"task/result","baseRefName":"trunk","isDraft":false,"mergeStateStatus":"CLEAN"}]' "$REMOTE_LAND_HEAD"
  ;;
 'pr create')
  touch "$REMOTE_LAND_STATE/created"
  if [ -f "$REMOTE_LAND_STATE/lose-create" ]; then rm "$REMOTE_LAND_STATE/lose-create"; exit 72; fi
  printf 'https://github.com/owner/repo/pull/42\n'
  ;;
 'pr checks') printf '[]' ;;
 'pr merge') "$REMOTE_LAND_REAL_GIT" -C "$REMOTE_LAND_PROJECT" push origin "$REMOTE_LAND_MERGED:refs/heads/trunk" >/dev/null 2>&1 ;;
 'pr view') printf '{"state":"MERGED","mergeCommit":{"oid":"%s"}}' "$REMOTE_LAND_MERGED" ;;
 *) exit 73 ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			if mode != "create" {
				var output bytes.Buffer
				if code := Run(context.Background(), []string{"--root", fixture.root, "land", "task-1"}, strings.NewReader(""), &output, &output); code == 0 {
					t.Fatal("unconfirmed PR preparation completed")
				}
				if mode == "conflicting branch" {
					if got := landingGit(t, fixture.remote, "rev-parse", "task/result"); got != base {
						t.Fatal("replaced a conflicting task branch")
					}
					if _, err := os.Stat(filepath.Join(directory, "created")); !os.IsNotExist(err) {
						t.Fatal("created a PR for a conflicting branch")
					}
					return
				}
			}
			got := invoke(t, fixture.root, "", "land", "task-1")
			if !strings.Contains(got, "commit: "+quote(merged)) {
				t.Fatal(got)
			}
			if got := landingGit(t, fixture.remote, "rev-parse", "task/result"); got != fixture.head {
				t.Fatal("primary pushed a different result")
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil || strings.Count(string(calls), "pr create ") != 1 {
				t.Fatal("duplicated or omitted PR creation")
			}
		})
	}
}
