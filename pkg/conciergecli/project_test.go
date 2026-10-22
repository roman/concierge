package conciergecli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestProjectShowOperatorSettings(t *testing.T) {
	for _, test := range []struct {
		name, config, repo, merge, delivery string
		code                                int
	}{
		{"absent", "", "repo", "off", "pr", 0},
		{"unconfigured project", `{"other":{"merge":"on","delivery":"fast-forward"}}`, "repo", "off", "pr", 0},
		{"autonomous PR", `{"repo":{"merge":"on"}}`, "repo", "on", "pr", 0},
		{"approved local", `{"repo":{"delivery":"fast-forward"}}`, "repo", "off", "fast-forward", 0},
		{"invalid merge", `{"repo":{"merge":"yes"}}`, "repo", "", "", 1},
		{"invalid delivery", `{"repo":{"delivery":"push"}}`, "repo", "", "", 1},
		{"unknown field", `{"repo":{"yolo":"on"}}`, "repo", "", "", 1},
		{"trailing JSON", `{} {}`, "repo", "", "", 1},
		{"null settings", `null`, "repo", "", "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.config != "" {
				if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "projects.json"), []byte(test.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"--root", root, "project", "show", test.repo},
				nil,
				&output,
				io.Discard,
			)
			if code != test.code {
				t.Fatalf("code %d: %s", code, &output)
			}
			if code == 0 {
				want := fmt.Sprintf(
					"repo: %q\nmerge: %s\ndelivery: %s\n",
					test.repo,
					test.merge,
					test.delivery,
				)
				if output.String() != want {
					t.Fatalf("want %q, got %q", want, output.String())
				}
			}
		})
	}
}

func TestProjectTargetBranch(t *testing.T) {
	for _, branch := range []string{"v3", "release/stable", "", "../bad", "--bad", "HEAD", "a b"} {
		t.Run(branch, func(t *testing.T) {
			root := t.TempDir()
			var output bytes.Buffer
			code := Run(
				t.Context(),
				[]string{"--root", root, "project", "set", "repo", "--branch=" + branch},
				nil,
				&output,
				io.Discard,
			)
			valid := branch == "v3" || branch == "release/stable" || branch == ""
			if valid {
				if code != 0 {
					t.Fatalf("set branch: %d %s", code, &output)
				}
				if branch != "" &&
					!strings.Contains(
						invoke(t, root, "", "project", "show", "repo"),
						"branch: "+quote(branch),
					) {
					t.Fatal("missing branch setting")
				}
			} else if code == 0 {
				t.Fatal("invalid branch accepted")
			}
		})
	}
}

func TestConfiguredBranchControlsLandingAndLaunchBase(t *testing.T) {
	f := newLandingFixture(t)
	landingGit(t, f.project, "push", "origin", "trunk:refs/heads/v3")
	defaultTip := landingGit(t, f.project, "rev-parse", "HEAD")
	invoke(t, f.root, "", "project", "set", "repo", "--branch=v3")
	got := invoke(t, f.root, "", "land", "task-1")
	if !strings.Contains(got, "default_branch: \"v3\"") {
		t.Fatal(got)
	}
	if landingGit(t, f.remote, "rev-parse", "v3") != f.head {
		t.Fatal("target did not advance")
	}
	if landingGit(t, f.remote, "rev-parse", "trunk") != defaultTip {
		t.Fatal("default branch changed")
	}
	command := launchCommand{TaskID: "task-1", Project: f.project}
	base, err := command.defaultBaseCommit(t.Context(), f.root)
	if err != nil || base != f.head {
		t.Fatalf("launch base: %s %v", base, err)
	}
	seedViewTask(t, f.root, "task-2", taskstate.Queued, nil, nil)
	store, _ := taskstate.NewStore(f.root)
	intent := taskstate.LaunchIntent{
		Generation: "gen-2",
		Name:       "worker-2",
		Label:      "task-2",
		Harness:    "codex",
		Session:    "work",
		Project:    f.project,
		Branch:     "task/new",
		BaseCommit: defaultTip,
		Body:       "Work",
	}
	if _, err := store.ReserveInitialLaunch(t.Context(), "task-2", intent); err != nil {
		t.Fatal(err)
	}
	command.TaskID = "task-2"
	base, err = command.defaultBaseCommit(t.Context(), f.root)
	if err != nil || base != defaultTip {
		t.Fatalf("retry changed captured base: %s %v", base, err)
	}
	invoke(t, f.root, "", "project", "set", "repo", "--branch=")
	command.TaskID = "task-1"
	base, err = command.defaultBaseCommit(t.Context(), f.root)
	if err != nil || base != defaultTip {
		t.Fatalf("default branch fallback: %s %v", base, err)
	}
}
