package conciergecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/gitremote"
	"github.com/roman/concierge/pkg/taskstate"
)

func landingGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, data, err)
	}
	return strings.TrimSpace(string(data))
}

type landingFixture struct {
	root, project, worktree, remote, head string
	worker                                taskstate.Worker
}

func newLandingFixture(t *testing.T) landingFixture {
	t.Helper()
	return landingFixtureAtRoot(t, shortTaskRoot(t))
}

func landingFixtureAtRoot(t *testing.T, root string) landingFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	directory := t.TempDir()
	f := landingFixture{
		root:     root,
		project:  filepath.Join(directory, "project"),
		worktree: filepath.Join(directory, "worker"),
		remote:   filepath.Join(directory, "remote"),
	}
	landingGit(t, directory, "init", "--bare", "--initial-branch=trunk", f.remote)
	landingGit(t, directory, "clone", f.remote, f.project)
	writeLandingFile(t, f.project, "tracked", "base")
	landingGit(t, f.project, "add", "tracked")
	landingGit(t, f.project, "commit", "-m", "base")
	base := landingGit(t, f.project, "rev-parse", "HEAD")
	landingGit(t, f.project, "push", "origin", "trunk")
	landingGit(t, f.project, "worktree", "add", "-b", "task/result", f.worktree)
	landingGit(t, f.worktree, "commit", "--allow-empty", "-m", "result")
	f.head = landingGit(t, f.worktree, "rev-parse", "HEAD")
	f.worker = taskstate.Worker{
		Name:       "worker",
		Harness:    "codex",
		Session:    "work",
		Pane:       "pane-1",
		Workspace:  "w1",
		Tab:        "t1",
		Project:    f.project,
		Worktree:   f.worktree,
		Generation: "gen-1",
		AgentArgs:  []string{"resume", "thread"},
		Lease: taskstate.WorktreeLease{
			ID:         "lease",
			Holder:     "task-1",
			Project:    f.project,
			Path:       f.worktree,
			Branch:     "task/result",
			BaseCommit: base,
			State:      taskstate.LeaseHeld,
		},
	}
	f.seed(
		t,
		&f.worker,
		&taskstate.Completion{
			Generation: "gen-1",
			Mode:       taskstate.Close,
			Phase:      taskstate.Accepted,
		},
	)
	invoke(t, f.root, "", "project", "set", "repo", "--merge", "on", "--delivery", "fast-forward")
	return f
}

func (f landingFixture) seed(
	t *testing.T,
	worker *taskstate.Worker,
	completion *taskstate.Completion,
) {
	t.Helper()
	seedViewTask(t, f.root, "task-1", taskstate.InFlight, worker, completion)
}

func TestLandFastForward(t *testing.T) {
	for _, test := range []struct {
		checkout, want string
	}{
		{"clean", "advanced"},
		{"local files", "advanced"},
		{"unstaged", "skipped: main checkout is dirty or unreadable"},
		{"staged", "skipped: main checkout is dirty or unreadable"},
		{"other branch", "skipped: main checkout is not on the default branch"},
		{"diverged", "skipped: main checkout cannot fast-forward"},
		{"untracked collision", "skipped: main checkout cannot fast-forward"},
	} {
		t.Run(test.checkout, func(t *testing.T) {
			f := newLandingFixture(t)
			if test.checkout == "untracked collision" {
				writeLandingFile(t, f.worktree, "collision", "landed")
				landingGit(t, f.worktree, "add", "collision")
				landingGit(t, f.worktree, "commit", "-m", "add collision")
				f.head = landingGit(t, f.worktree, "rev-parse", "HEAD")
			}
			landingGit(t, f.worktree, "push", "origin", "task/result")
			switch test.checkout {
			case "local files":
				writeLandingFile(t, f.project, ".git/info/exclude", "ignored\n")
				writeLandingFile(t, f.project, "ignored", "keep ignored")
				writeLandingFile(t, f.project, "untracked", "keep untracked")
			case "unstaged", "staged":
				writeLandingFile(t, f.project, "tracked", "local change")
				if test.checkout == "staged" {
					landingGit(t, f.project, "add", "tracked")
				}
			case "other branch":
				landingGit(t, f.project, "checkout", "-b", "other")
			case "diverged":
				landingGit(t, f.project, "commit", "--allow-empty", "-m", "local only")
			case "untracked collision":
				writeLandingFile(t, f.project, "collision", "keep local")
			}
			before := landingGit(t, f.project, "rev-parse", "HEAD")
			got := invoke(t, f.root, "", "land", "task-1")
			if !strings.Contains(got, "commit: \""+f.head+"\"") ||
				!strings.Contains(got, "default_branch: \"trunk\"") {
				t.Fatal(got)
			}
			if landed := landingGit(t, f.remote, "rev-parse", "trunk"); landed != f.head {
				t.Fatal(landed)
			}
			if branches := landingGit(t, f.remote, "for-each-ref", "refs/heads/task/result"); branches != "" {
				t.Fatal(branches)
			}
			want := before
			if test.want == "advanced" {
				want = f.head
			}
			if head := landingGit(t, f.project, "rev-parse", "HEAD"); head != want {
				t.Fatalf("checkout moved: %s, want %s", head, want)
			}
			if !strings.Contains(got, "checkout: "+quote(test.want)) {
				t.Fatal(got)
			}
			ledger, err := readOperatorLedger(filepath.Join(f.root, "operator-actions.json"))
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "advanced" {
				if len(ledger.Actions) != 0 {
					t.Fatal(ledger.Actions)
				}
				invoke(t, f.root, "", "land", "task-1")
			} else {
				if len(ledger.Actions) != 1 {
					t.Fatal(ledger.Actions)
				}
				for _, text := range ledger.Actions {
					if !strings.Contains(text, f.project) || !strings.Contains(text, test.want) ||
						!strings.Contains(text, "git -C '"+f.project+"' pull --ff-only 'origin' 'trunk'") {
						t.Fatal(text)
					}
				}
			}
			if test.checkout == "local files" || test.checkout == "untracked collision" {
				file, want := "untracked", "keep untracked"
				if test.checkout == "untracked collision" {
					file, want = "collision", "keep local"
				}
				data, err := os.ReadFile(filepath.Join(f.project, file))
				if err != nil || string(data) != want {
					t.Fatalf("local file: %q %v", data, err)
				}
			}
		})
	}
}

func writeLandingFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLandResolvesCheckoutAction(t *testing.T) {
	f := newLandingFixture(t)
	invoke(t, f.root, "", "operator", "ask", "unrelated", "Keep this request")
	writeLandingFile(t, f.project, "tracked", "local change")
	invoke(t, f.root, "", "land", "task-1")
	first, err := readOperatorLedger(filepath.Join(f.root, "operator-actions.json"))
	if err != nil || len(first.Actions) != 2 {
		t.Fatalf("first skip: %+v %v", first, err)
	}
	invoke(t, f.root, "", "operator", "remind")
	landingGit(t, f.project, "checkout", "-b", "other")
	invoke(t, f.root, "", "land", "task-1")
	second, err := readOperatorLedger(filepath.Join(f.root, "operator-actions.json"))
	if err != nil || len(second.Actions) != 2 {
		t.Fatalf("second skip: %+v %v", second, err)
	}
	for id := range first.Actions {
		if _, found := second.Actions[id]; !found {
			t.Fatal("checkout action ID changed")
		}
	}
	landingGit(t, f.project, "checkout", "trunk")
	landingGit(t, f.project, "restore", "tracked")
	invoke(t, f.root, "", "land", "task-1")
	resolved, err := readOperatorLedger(filepath.Join(f.root, "operator-actions.json"))
	if err != nil || len(resolved.Actions) != 1 ||
		resolved.Actions["unrelated"] != "Keep this request" {
		t.Fatalf("resolution: %+v %v", resolved, err)
	}
	for key := range resolved.Reminded {
		if strings.HasPrefix(key, "action:") && key != "action:unrelated" {
			t.Fatal("checkout reminder remains: " + key)
		}
	}
}

func TestLandRefusals(t *testing.T) {
	for _, reason := range []string{"merge off", "not accepted", "retired", "no worker", "no lease", "wrong branch", "detached", "dirty", "diverged", "wrong repository", "missing worktree", "default branch"} {
		t.Run(reason, func(t *testing.T) {
			f := newLandingFixture(t)
			switch reason {
			case "merge off":
				invoke(t, f.root, "", "project", "set", "repo", "--merge", "off")
			case "not accepted":
				f.seed(t, &f.worker, nil)
			case "retired":
				f.seed(
					t,
					&f.worker,
					&taskstate.Completion{
						Generation: "gen-1",
						Mode:       taskstate.Close,
						Phase:      taskstate.Retired,
					},
				)
			case "no worker":
				seedViewTask(t, f.root, "task-1", taskstate.Queued, nil, nil)
			case "no lease":
				f.worker.Lease = taskstate.WorktreeLease{}
				f.seed(
					t,
					&f.worker,
					&taskstate.Completion{
						Generation: "gen-1",
						Mode:       taskstate.Close,
						Phase:      taskstate.Accepted,
					},
				)
			case "wrong branch":
				landingGit(t, f.worktree, "checkout", "-b", "wrong")
			case "detached":
				landingGit(t, f.worktree, "checkout", "--detach")
			case "dirty":
				if err := os.WriteFile(filepath.Join(f.worktree, "dirty"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
			case "diverged":
				landingGit(t, f.project, "commit", "--allow-empty", "-m", "remote advance")
				landingGit(t, f.project, "push", "origin", "trunk")
			case "wrong repository":
				landingGit(t, f.project, "worktree", "remove", f.worktree)
				landingGit(t, filepath.Dir(f.worktree), "clone", f.remote, f.worktree)
				landingGit(t, f.worktree, "checkout", "-b", "task/result")
			case "missing worktree":
				landingGit(t, f.project, "worktree", "remove", f.worktree)
			case "default branch":
				landingGit(t, f.project, "worktree", "remove", f.worktree)
				landingGit(t, f.project, "checkout", "--detach")
				landingGit(t, f.project, "worktree", "add", f.worktree, "trunk")
				f.worker.Lease.Branch = "trunk"
				f.seed(
					t,
					&f.worker,
					&taskstate.Completion{
						Generation: "gen-1",
						Mode:       taskstate.Close,
						Phase:      taskstate.Accepted,
					},
				)
			}
			before := landingGit(t, f.remote, "rev-parse", "trunk")
			expectLifecycleError(t, f.root, 1, "land", "task-1")
			if head := landingGit(t, f.remote, "rev-parse", "trunk"); head != before {
				t.Fatal("refusal changed remote")
			}
		})
	}
}

func TestLandPreservesIgnoredWorkerFiles(t *testing.T) {
	f := newLandingFixture(t)
	writeLandingFile(t, f.project, ".git/info/exclude", "generated\n")
	writeLandingFile(t, f.worktree, "generated", "keep shell output")
	invoke(t, f.root, "", "land", "task-1")
	data, err := os.ReadFile(filepath.Join(f.worktree, "generated"))
	if err != nil || string(data) != "keep shell output" {
		t.Fatalf("ignored file changed: %q %v", data, err)
	}
	if landed := landingGit(t, f.remote, "rev-parse", "trunk"); landed != f.head {
		t.Fatal(landed)
	}
}

func TestLandPreservesIgnoredCheckoutCollision(t *testing.T) {
	f := newLandingFixture(t)
	writeLandingFile(t, f.worktree, "collision", "committed")
	landingGit(t, f.worktree, "add", "collision")
	landingGit(t, f.worktree, "commit", "-m", "result file")
	writeLandingFile(t, f.project, ".git/info/exclude", "collision\n")
	writeLandingFile(t, f.project, "collision", "keep local output")
	got := invoke(t, f.root, "", "land", "task-1")
	if !strings.Contains(got, "skipped: main checkout cannot fast-forward") {
		t.Fatal(got)
	}
	data, err := os.ReadFile(filepath.Join(f.project, "collision"))
	if err != nil || string(data) != "keep local output" {
		t.Fatalf("ignored collision overwritten: %q %v", data, err)
	}
}

func TestLandOperatorApproval(t *testing.T) {
	f := newLandingFixture(t)
	invoke(t, f.root, "", "project", "set", "repo", "--merge", "off")
	invoke(t, f.root, "", "land", "task-1", "--operator-approved")
	if head := landingGit(t, f.remote, "rev-parse", "trunk"); head != f.head {
		t.Fatal(head)
	}
}

func TestLandHoldAndRemoteFailure(t *testing.T) {
	for _, reason := range []string{"hold", "push refused", "no default branch"} {
		t.Run(reason, func(t *testing.T) {
			f := newLandingFixture(t)
			before := landingGit(t, f.remote, "rev-parse", "trunk")
			switch reason {
			case "hold":
				store, _ := taskstate.NewStore(f.root)
				current, err := store.Read(context.Background(), "task-1")
				if err != nil {
					t.Fatal(err)
				}
				snapshot := current.Snapshot()
				snapshot.Definition.Hold = &taskstate.Hold{Reason: "Operator decision"}
				task, err := taskstate.NewMigrated(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				data, err := task.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.root, "records", "task-1.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "push refused":
				if err := os.WriteFile(filepath.Join(f.remote, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "no default branch":
				landingGit(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/missing")
			}
			expectLifecycleError(t, f.root, 1, "land", "task-1")
			if head := landingGit(t, f.remote, "rev-parse", "trunk"); head != before {
				t.Fatal("refusal changed remote")
			}
		})
	}
}

func TestLandPreservesDifferentRemoteTaskBranch(t *testing.T) {
	f := newLandingFixture(t)
	landingGit(t, f.worktree, "push", "origin", "task/result")
	landingGit(t, f.worktree, "commit", "--allow-empty", "-m", "local result")
	got := invoke(t, f.root, "", "land", "task-1")
	if !strings.Contains(got, "remote_branch: \"skipped:") {
		t.Fatal(got)
	}
	if head := landingGit(t, f.remote, "rev-parse", "task/result"); head != f.head {
		t.Fatal("remote task branch changed")
	}
}

func TestLandingRepository(t *testing.T) {
	for _, test := range []struct{ remote, want string }{
		{"git@github.com:owner/repo.git", "github.com/owner/repo"},
		{"https://github.com/owner/repo.git", "github.com/owner/repo"},
		{"ssh://git@forge.example/owner/repo", "forge.example/owner/repo"},
		{"/tmp/repository", ""}, {"https://github.com/repo", ""},
	} {
		got, err := gitremote.Repository(test.remote)
		if got != test.want || (err != nil) != (test.want == "") {
			t.Fatalf("%s: %s %v", test.remote, got, err)
		}
	}
}

func TestLandRefusesDifferentPushEndpoint(t *testing.T) {
	f := newLandingFixture(t)
	other := filepath.Join(t.TempDir(), "other.git")
	landingGit(t, filepath.Dir(other), "clone", "--bare", f.remote, other)
	landingGit(t, f.project, "remote", "set-url", "--push", "origin", other)
	expectLifecycleError(t, f.root, 1, "land", "task-1")
	if head := landingGit(t, other, "rev-parse", "trunk"); head == f.head {
		t.Fatal("pushed to an uninspected remote")
	}
}

func TestLandRefusesScout(t *testing.T) {
	f := newLandingFixture(t)
	store, _ := taskstate.NewStore(f.root)
	current, err := store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := current.Snapshot()
	snapshot.Definition.Kind = "scout"
	task, err := taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	before := landingGit(t, f.remote, "rev-parse", "trunk")
	expectLifecycleError(t, f.root, 1, "land", "task-1")
	if head := landingGit(t, f.remote, "rev-parse", "trunk"); head != before {
		t.Fatal("landed a scout branch")
	}
}

func TestLandDeletionRejectsConcurrentBranchChange(t *testing.T) {
	f := newLandingFixture(t)
	landingGit(t, f.worktree, "push", "origin", "task/result")
	landingGit(t, f.worktree, "commit", "--allow-empty", "-m", "later")
	later := landingGit(t, f.worktree, "rev-parse", "HEAD")
	landingGit(t, f.worktree, "push", "origin", later+":refs/heads/concurrent")
	landingGit(t, f.worktree, "reset", "--hard", f.head)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	t.Setenv("LAND_REAL_GIT", git)
	t.Setenv("LAND_REMOTE", f.remote)
	t.Setenv("LAND_LATER", later)
	script := `#!/bin/sh
case "$*" in
 'push origin --delete task/result'*) "$LAND_REAL_GIT" --git-dir="$LAND_REMOTE" update-ref refs/heads/task/result "$LAND_LATER" ;;
esac
exec "$LAND_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := invoke(t, f.root, "", "land", "task-1")
	if !strings.Contains(got, "remote_branch: \"skipped:") {
		t.Fatal(got)
	}
	if head := landingGit(t, f.remote, "rev-parse", "task/result"); head != later {
		t.Fatal("deleted a concurrent branch change")
	}
}

func TestLandPullRequest(t *testing.T) {
	for _, outcome := range []string{"green", "red", "pending", "missing", "wrong head", "wrong base", "draft", "blocked", "unconfirmed", "read error", "null checks", "missing merge commit"} {
		t.Run(outcome, func(t *testing.T) {
			f := newLandingFixture(t)
			invoke(t, f.root, "", "project", "set", "repo", "--delivery", "pr")
			directory := t.TempDir()
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAND_REAL_GIT", git)
			transport := `#!/bin/sh
case "$*" in
 'remote get-url --all origin'|'remote get-url --push --all origin') printf '%s\n' 'https://github.com/owner/repo.git' ;;
 *) exec "$LAND_REAL_GIT" "$@" ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "git"), []byte(transport), 0700); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(directory, "calls")
			t.Setenv("LAND_GH_RECORD", record)
			t.Setenv("LAND_HEAD", f.head)
			t.Setenv("LAND_OUTCOME", outcome)
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$LAND_GH_RECORD"
case "$1 $2" in
 'pr list')
  [ "$LAND_OUTCOME" != 'read error' ] || exit 1
  [ "$LAND_OUTCOME" != missing ] || { printf '[]'; exit; }
  head=$LAND_HEAD; base=trunk; draft=false; status=CLEAN
  [ "$LAND_OUTCOME" != 'wrong head' ] || head=wrong
  [ "$LAND_OUTCOME" != 'wrong base' ] || base=other
  [ "$LAND_OUTCOME" != draft ] || draft=true
  [ "$LAND_OUTCOME" != blocked ] || status=BLOCKED
  printf '[{"number":42,"url":"https://github.com/owner/repo/pull/42","headRefOid":"%s","headRefName":"task/result","baseRefName":"%s","isDraft":%s,"mergeStateStatus":"%s"}]' "$head" "$base" "$draft" "$status" ;;
 'pr checks')
  [ "$LAND_OUTCOME" != 'null checks' ] || { printf null; exit; }
  bucket=pass; state=SUCCESS
  [ "$LAND_OUTCOME" != red ] || { bucket=fail; state=FAILURE; }
  [ "$LAND_OUTCOME" != pending ] || { bucket=pending; state=IN_PROGRESS; }
  printf '[{"name":"test","bucket":"%s","state":"%s"}]' "$bucket" "$state" ;;
 'pr merge') exit 0 ;;
 'pr view')
  [ "$LAND_OUTCOME" != 'missing merge commit' ] || { printf '{"state":"MERGED"}'; exit; }
  state=MERGED
  [ "$LAND_OUTCOME" != unconfirmed ] || state=OPEN
  printf '{"state":"%s","mergeCommit":{"oid":"landed-commit"}}' "$state" ;;
 *) exit 71 ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			if outcome == "green" {
				got := invoke(t, f.root, "", "land", "task-1")
				if !strings.Contains(got, "https://github.com/owner/repo/pull/42") {
					t.Fatal(got)
				}
				if !strings.Contains(got, "commit: \"landed-commit\"") {
					t.Fatal(got)
				}
			} else {
				expectLifecycleError(t, f.root, 1, "land", "task-1")
			}
			calls, _ := os.ReadFile(record)
			if !strings.Contains(string(calls), "--repo github.com/owner/repo") {
				t.Fatal("missing repository binding: " + string(calls))
			}
			if outcome == "green" || outcome == "unconfirmed" || outcome == "missing merge commit" {
				if !strings.Contains(string(calls), "--match-head-commit "+f.head) {
					t.Fatal(string(calls))
				}
			} else if strings.Contains(string(calls), "pr merge") {
				t.Fatal("merged refused PR: " + string(calls))
			}
		})
	}
}
