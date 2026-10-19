package taskops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
	"github.com/roman/tincan/pkg/inbox"
)

type remoteResultExporterFixture struct {
	source  string
	git     worktree.Client
	commit  string
	calls   int
	failure error
}

func TestSelectedRemoteResultRefusesNewAssignments(t *testing.T) {
	service, exporter, directory := remoteResultMaterializationFixture(t)
	ctx := context.Background()
	if _, err := service.store.SelectRemoteResult(ctx, "task-1", "gen-1", exporter.commit, "result-holder"); err != nil {
		t.Fatal(err)
	}
	worker, err := service.SupervisedWorker(
		"task-1",
		filepath.Join(directory, "messages"),
		herdr.Client{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = worker.sendAssignment(
		ctx,
		"new-work",
		"New work",
		func(inbox.Recipient, string, string) (inbox.Message, error) {
			t.Fatal("published new work while materializing an accepted selection")
			return inbox.Message{}, nil
		},
	)
	if !errors.Is(err, taskstate.ErrInvalidTransition) {
		t.Fatalf("selected worker accepted new work: %v", err)
	}
}

func (fixture *remoteResultExporterFixture) Export(
	ctx context.Context,
	task taskstate.Task,
	selected string,
) (*worktree.CommitBundle, error) {
	fixture.calls++
	if fixture.failure != nil {
		return nil, fixture.failure
	}
	if task.Snapshot().Worker == nil || (selected != "" && selected != fixture.commit) {
		return nil, errors.New("changed result selection")
	}
	return fixture.git.ExportCommit(ctx, fixture.source, fixture.commit)
}

func remoteResultMaterializationFixture(
	t *testing.T,
) (*Service, *remoteResultExporterFixture, string) {
	t.Helper()
	service, intent, config, _ := remoteInitialLeaseFixture(t)
	prepared, err := service.prepareRemoteInitialLease(
		context.Background(),
		"task-1",
		intent,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	lease := prepared.Lease
	current, err := service.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := current.Snapshot()
	snapshot.State, snapshot.LaunchIntent = taskstate.InFlight, nil
	snapshot.Worker = &taskstate.Worker{
		Name:       intent.Name,
		Harness:    intent.Harness,
		Machine:    intent.Machine,
		Session:    intent.Session,
		Workspace:  "workspace",
		Tab:        "tab-1",
		Pane:       "pane-1",
		Project:    lease.Project,
		Worktree:   lease.Path,
		Generation: intent.Generation,
		AgentArgs:  []string{"resume", "native-id"},
		Lease:      lease,
	}
	task, err := taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "remote-result")
	runGit := func(project string, args ...string) string {
		t.Helper()
		data, err := exec.Command(config.Git.Executable, append([]string{"-C", project}, args...)...).
			CombinedOutput()
		if err != nil {
			t.Fatalf("Git result fixture: %s %v", data, err)
		}
		return strings.TrimSpace(string(data))
	}
	runGit(intent.Remote.PrimaryProject, "clone", "--", intent.Remote.PrimaryProject, source)
	runGit(
		source,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Remote ship result",
	)
	exporter := &remoteResultExporterFixture{
		source: source,
		git:    config.Git,
		commit: runGit(source, "rev-parse", "HEAD"),
	}
	pool := filepath.Join(directory, "treehouse")
	t.Setenv("RESULT_TEST_POOL", directory)
	script := `#!/bin/sh
set -eu
case "$1" in
get)
  printf 'get\n' >> "$RESULT_TEST_POOL/gets"
  shift
  holder=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --lease-holder ]; then holder=$2; shift; fi
    shift
  done
  git worktree add --detach "$RESULT_TEST_POOL/lease" HEAD >/dev/null 2>&1
  printf '{"path":"%s/lease","lease_id":"local-lease","lease_holder":"%s","flavor":"git","processes":[]}' "$RESULT_TEST_POOL" "$holder" > "$RESULT_TEST_POOL/inventory"
  if [ -f "$RESULT_TEST_POOL/lose-acquisition" ]; then exit 72; fi
  cat "$RESULT_TEST_POOL/inventory"
  ;;
status)
  if [ -f "$RESULT_TEST_POOL/inventory" ]; then
    printf '['; cat "$RESULT_TEST_POOL/inventory"; printf ']'
  else printf '[]'; fi
  ;;
return)
  printf 'return\n' >> "$RESULT_TEST_POOL/returns"
  printf '{"path":"%s/lease","lease_id":"","lease_holder":"","flavor":"git","processes":[]}' "$RESULT_TEST_POOL" > "$RESULT_TEST_POOL/inventory"
  if [ -f "$RESULT_TEST_POOL/lose-return" ]; then exit 72; fi
  ;;
*) exit 73 ;;
esac
`
	if err := os.WriteFile(pool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	service.git, service.pool = config.Git, worktree.PoolClient{Executable: pool}
	return service.WithRemoteResultExporter(exporter), exporter, directory
}

func TestRemoteResultMaterializationRecoversWithoutAnotherPrimaryLease(t *testing.T) {
	for _, mode := range []string{"complete", "lost acquisition", "lost acquisition remote unavailable"} {
		t.Run(mode, func(t *testing.T) {
			service, exporter, directory := remoteResultMaterializationFixture(t)
			ctx := context.Background()
			before, err := service.store.Read(ctx, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			original := *before.Snapshot().Worker
			if strings.HasPrefix(mode, "lost acquisition") {
				if err := os.WriteFile(filepath.Join(directory, "lose-acquisition"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := service.MaterializeRemoteResult(ctx, "task-1", original.Generation); err == nil {
					t.Fatal("confirmed a lost primary acquisition")
				}
				pending, err := service.store.Read(ctx, "task-1")
				if err != nil ||
					pending.Snapshot().Worker.RemoteResult.Phase != taskstate.RemoteResultLeaseRequested {
					t.Fatal("lost pending primary holder")
				}
				if err := os.Remove(filepath.Join(directory, "lose-acquisition")); err != nil {
					t.Fatal(err)
				}
				if mode == "lost acquisition remote unavailable" {
					exporter.failure = errors.New("remote now unavailable")
				}
			}
			ready, err := service.MaterializeRemoteResult(ctx, "task-1", original.Generation)
			if err != nil {
				t.Fatal(err)
			}
			bound := ready.Snapshot().Worker
			result := bound.RemoteResult
			if result.Phase != taskstate.RemoteResultReady || result.Commit != exporter.commit ||
				result.Lease.State != taskstate.LeaseHeld ||
				bound.Project != original.Project ||
				bound.Worktree != original.Worktree ||
				bound.Lease != original.Lease ||
				bound.Generation != original.Generation {
				t.Fatalf("materialization changed the remote worker or selected result: %+v", bound)
			}
			exporter.failure = errors.New("remote now unavailable")
			if _, err := service.MaterializeRemoteResult(ctx, "task-1", original.Generation); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(directory, "gets")); err != nil ||
				string(data) != "get\n" {
				t.Fatal("acquired another primary worktree")
			}
			if err := service.git.PrepareLeased(ctx, worktree.Specification{Project: result.Lease.Project, Path: result.Lease.Path, Branch: result.Lease.Branch, BaseCommit: result.Commit}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoteResultRequiresPrimaryToolsBeforeSelection(t *testing.T) {
	for _, missing := range []string{"git", "pool"} {
		t.Run(missing, func(t *testing.T) {
			service, exporter, _ := remoteResultMaterializationFixture(t)
			if missing == "git" {
				service.git.Executable = ""
			} else {
				service.pool.Executable = ""
			}
			ctx := context.Background()
			_, err := service.MaterializeRemoteResult(ctx, "task-1", "gen-1")
			if !errors.Is(err, ErrRemoteResultUnavailable) || exporter.calls != 0 {
				t.Fatalf(
					"missing primary tools reached remote export: calls=%d err=%v",
					exporter.calls,
					err,
				)
			}
			current, err := service.store.Read(ctx, "task-1")
			if err != nil || current.Snapshot().Worker.RemoteResult != (taskstate.RemoteResult{}) {
				t.Fatal("missing tools selected a result")
			}
		})
	}
}
