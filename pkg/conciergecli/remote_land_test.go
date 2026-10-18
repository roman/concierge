package conciergecli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func remoteLandingFixture(t *testing.T) landingFixture {
	t.Helper()
	root := shortTaskRoot(t)
	backup := root + "-backup"
	t.Cleanup(func() { _ = os.RemoveAll(backup) })
	if err := taskstate.UpgradeVersionTwo(context.Background(), root, backup); err != nil {
		t.Fatal(err)
	}
	fixture := landingFixtureAtRoot(t, root)
	primary := fixture.worker.Lease
	primary.BaseCommit = fixture.head
	location := taskstate.RemoteLease{
		Machine:        "machine-1",
		Target:         "synthetic-machine",
		PrimaryRoot:    fixture.root,
		PrimaryProject: fixture.project,
		Base:           "/remote/base",
		Repository:     "repo",
		ClientCommit:   strings.Repeat("b", 40),
		Backend:        taskstate.RemoteGit,
	}
	remote := fixture.worker.Lease
	remote.ID, remote.Holder = "remote-lease", "remote-holder"
	remote.Remote, remote.Project, remote.Path = location, location.ProjectPath(), location.PoolRoot()+"/slot/repo"
	fixture.worker.Machine, fixture.worker.Project, fixture.worker.Worktree = location.Machine, remote.Project, remote.Path
	fixture.worker.Lease = remote
	fixture.worker.RemoteResult = taskstate.RemoteResult{
		Commit: fixture.head,
		Holder: primary.Holder,
		Phase:  taskstate.RemoteResultReady,
		Lease:  primary,
	}
	fixture.seed(
		t,
		&fixture.worker,
		&taskstate.Completion{
			Generation: "gen-1",
			Mode:       taskstate.Close,
			Phase:      taskstate.Accepted,
		},
	)
	return fixture
}

func TestRemoteLandUsesHeldPrimaryLeaseAndSelectedCommit(t *testing.T) {
	fixture := remoteLandingFixture(t)
	got := invoke(t, fixture.root, "", "land", "task-1")
	if !strings.Contains(got, "commit: "+quote(fixture.head)) {
		t.Fatal(got)
	}
	if head := landingGit(t, fixture.remote, "rev-parse", "trunk"); head != fixture.head {
		t.Fatal("primary did not land the selected remote commit")
	}
	if branches := landingGit(t, fixture.remote, "for-each-ref", "refs/heads/task/result"); branches != "" {
		t.Fatal("fast-forward delivery pushed a remote task branch")
	}
	store, err := taskstate.NewStore(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Read(context.Background(), "task-1")
	if err != nil || current.Snapshot().Worker.Worktree != fixture.worker.Worktree ||
		current.Snapshot().Worker.Lease != fixture.worker.Lease {
		t.Fatal("landing changed the native supervision binding")
	}
}

func TestRemoteLandRejectsChangedPrimaryHeadAndMissingMaterialization(t *testing.T) {
	for _, mode := range []string{"changed head", "missing materialization", "foreign root"} {
		t.Run(mode, func(t *testing.T) {
			fixture := remoteLandingFixture(t)
			base := landingGit(t, fixture.remote, "rev-parse", "trunk")
			switch mode {
			case "changed head":
				landingGit(
					t,
					fixture.worktree,
					"commit",
					"--allow-empty",
					"-m",
					"Unaccepted local change",
				)
			case "missing materialization":
				fixture.worker.RemoteResult = taskstate.RemoteResult{}
			case "foreign root":
				fixture.worker.Lease.Remote.PrimaryRoot = "/foreign/state"
			}
			fixture.seed(
				t,
				&fixture.worker,
				&taskstate.Completion{
					Generation: "gen-1",
					Mode:       taskstate.Close,
					Phase:      taskstate.Accepted,
				},
			)
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"--root", fixture.root, "land", "task-1"},
				strings.NewReader(""),
				&output,
				&output,
			)
			if code == 0 {
				t.Fatal("landed an unaccepted or foreign remote result")
			}
			if head := landingGit(t, fixture.remote, "rev-parse", "trunk"); head != base {
				t.Fatal("failed landing changed the remote default branch")
			}
		})
	}
}
