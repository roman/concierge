package hostservice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestNativeCleanupRequiresMatchingLandedResultIncludingSquash(t *testing.T) {
	for _, mode := range []string{"fast-forward", "squash", "wrong PR head", "unlanded merge", "unavailable PR", "oversized PR", "retained abandonment"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRemoteActivationFixture(t)
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_AUTHOR_NAME", "Test")
			t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
			t.Setenv("GIT_COMMITTER_NAME", "Test")
			t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			fixture.server.config.GitExecutable = git
			project := t.TempDir()
			runGit := func(args ...string) string {
				t.Helper()
				data, err := exec.Command(git, append([]string{"-C", project}, args...)...).
					CombinedOutput()
				if err != nil {
					t.Fatalf("Git proof fixture: %s %v", data, err)
				}
				return strings.TrimSpace(string(data))
			}
			runGit("init", "-b", "main")
			runGit("commit", "--allow-empty", "-m", "Base")
			base := runGit("rev-parse", "HEAD")
			origin := filepath.Join(t.TempDir(), "origin.git")
			runGit("clone", "--bare", project, origin)
			url := "https://github.com/owner/repo.git"
			runGit("remote", "add", "origin", url)
			runGit("config", "url."+origin+".insteadOf", url)
			runGit("commit", "--allow-empty", "-m", "Accepted result")
			selected := runGit("rev-parse", "HEAD")
			merged := runGit("commit-tree", selected+"^{tree}", "-p", base, "-m", "Squash result")
			landed := merged
			if mode == "fast-forward" {
				landed = selected
			}
			if mode != "unlanded merge" && mode != "retained abandonment" {
				runGit("push", "origin", landed+":refs/heads/main")
			}
			current, err := fixture.server.store.Read(context.Background(), "task-1")
			if err != nil {
				t.Fatal(err)
			}
			current, err = current.CompleteInitialLaunch("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := current.Snapshot()
			snapshot.Worker.Lease.Remote.PrimaryProject = project
			snapshot.Worker.Lease.BaseCommit = base
			snapshot.Worker.RemoteResult = taskstate.RemoteResult{
				Commit: selected,
				Holder: "primary-holder",
				Phase:  taskstate.RemoteResultReady,
				Lease: taskstate.WorktreeLease{
					ID:         "primary-lease",
					Holder:     "primary-holder",
					Project:    project,
					Path:       filepath.Join(project, "primary-lease"),
					Branch:     snapshot.Worker.Lease.Branch,
					BaseCommit: selected,
					State:      taskstate.LeaseHeld,
				},
			}
			current, err = taskstate.NewMigrated(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "retained abandonment" {
				current, err = current.PlaceHold(
					taskstate.Hold{Reason: "Keep the task for follow-up"},
				)
				if err != nil {
					t.Fatal(err)
				}
				current, err = current.AcceptResult("gen-1", taskstate.Retain, "")
			} else {
				current, err = current.AcceptResult("gen-1", taskstate.Close, "2026-10-17")
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err = current.RetireResult("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			lease, _ := current.RetiredLease("gen-1")
			configuration := map[string]machineprep.Configuration{
				fixture.profile.ID: {
					Target: fixture.profile.Target,
					Base:   lease.Remote.Base,
					Agent:  "codex",
					Repositories: []machineprep.Repository{
						{Name: "concierge", URL: "https://github.com/owner/concierge.git"},
						{Name: current.Definition().Repo, URL: url},
					},
				},
			}
			data, err := json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(fixture.server.config.Root, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fixture.server.config.Root, "config", "machines.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			tools := t.TempDir()
			prHead := selected
			if mode == "wrong PR head" {
				prHead = base
			}
			t.Setenv(
				"PROOF_PR_JSON",
				"[{\"state\":\"MERGED\",\"headRefOid\":\""+prHead+"\",\"headRefName\":\""+lease.Branch+"\",\"baseRefName\":\"main\",\"mergeCommit\":{\"oid\":\""+merged+"\"}}]",
			)
			script := "#!/bin/sh\nprintf '%s' \"$PROOF_PR_JSON\"\n"
			if mode == "unavailable PR" {
				script = "#!/bin/sh\nexit 72\n"
			}
			if mode == "oversized PR" {
				script = "#!/bin/sh\nexec head -c 70000 /dev/zero\n"
			}
			if err := os.WriteFile(filepath.Join(tools, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			proof, err := (hostRemoteLeases{server: fixture.server}).resultReturnProof(
				context.Background(),
				current,
				"gen-1",
				lease,
				fixture.profile,
			)
			if mode == "fast-forward" || mode == "squash" {
				if err != nil || proof.Outcome != remoteworker.WorktreeLanded ||
					proof.Commit != selected {
					t.Fatalf("native result proof changed the selected HEAD: %+v %v", proof, err)
				}
			} else if mode == "retained abandonment" {
				if err != nil || proof.Outcome != remoteworker.WorktreeAbandoned || proof.Commit != selected {
					t.Fatalf("explicit retained release lost the selected abandonment proof: %+v %v", proof, err)
				}
			} else if err == nil {
				t.Fatal("cleanup accepted an unconfirmed or different result")
			}
		})
	}
}
