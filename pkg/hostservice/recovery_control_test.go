package hostservice

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/taskstate"
)

func blockedRecoveryFixture(t *testing.T) (*Server, LaunchRequest, taskstate.Snapshot) {
	t.Helper()
	server, input := launchRequestFixture(t)
	input.Worktree = ""
	server.config.Diagnostics = io.Discard
	ctx, cancel := context.WithCancel(t.Context())
	task, err := server.reserveInitialLaunch(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	generation := task.Snapshot().LaunchIntent.Generation
	lease := taskstate.WorktreeLease{
		ID: "lease-1", Holder: "holder", Project: input.Project, Path: t.TempDir(),
		Branch: input.Branch, BaseCommit: input.BaseCommit, State: taskstate.LeaseHeld,
	}
	if _, err := server.store.Update(ctx, input.TaskID, func(task taskstate.Task) (taskstate.Task, error) {
		return task.BeginLaunchPreparation(generation)
	}); err != nil {
		t.Fatalf("begin preparation: %v", err)
	}
	if _, err := server.store.RequestLaunchLease(ctx, input.TaskID, generation, lease.Holder); err != nil {
		t.Fatalf("request lease: %v", err)
	}
	if _, err := server.store.RecordLaunchLease(ctx, input.TaskID, generation, lease); err != nil {
		t.Fatalf("record lease: %v", err)
	}
	if _, err := server.store.Update(ctx, input.TaskID, func(task taskstate.Task) (taskstate.Task, error) {
		return task.RequestLaunchPane(generation)
	}); err != nil {
		t.Fatalf("request pane: %v", err)
	}
	task, err = server.store.RecordLaunchEndpoint(
		ctx,
		input.TaskID,
		generation,
		taskstate.LaunchEndpoint{
			Workspace: input.Workspace, Tab: "tab-2", Pane: "pane-2",
		},
	)
	if err != nil {
		t.Fatalf("record endpoint: %v", err)
	}
	input.Worktree = lease.Path
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("RECOVERY_CONTROL_STARTED", marker)
	git := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(git, []byte(`#!/bin/sh
printf started > "$RECOVERY_CONTROL_STARTED"
exec sleep 120
`), 0700); err != nil {
		t.Fatal(err)
	}
	server.config.GitExecutable = git
	server.config.TreehouseExecutable = git
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(`#!/bin/sh
case "$1 $2" in
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	key := controllerKey{kind: taskController, id: input.TaskID}
	if err := server.controllers.awaitInitialWakeup(key); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var recoveryError error
	go func() {
		recoveryError = server.reconcileInitialTask(ctx, key)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("recovery did not join")
		}
	})
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("recovery stopped before the blocking launch call: %v", recoveryError)
		case <-deadline:
			t.Fatal("recovery did not reach the blocking launch call")
		case <-time.After(time.Millisecond):
		}
	}
	return server, input, task.Snapshot()
}

func TestAbandonCancelsAbsentLaunchRecovery(t *testing.T) {
	server, input, before := blockedRecoveryFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := server.abandonLaunch(ctx, TransitionRequest{
		TaskID: input.TaskID, Generation: before.LaunchIntent.Generation,
	})
	if err != nil || response.Abandoned == nil {
		t.Fatalf("abandon during recovery: %+v, %v", response, err)
	}
	after, err := server.store.Read(ctx, input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	expected := before
	expected.LaunchIntent = nil
	expected.AbandonedLaunches = []taskstate.LaunchAbandonment{{
		Generation: before.LaunchIntent.Generation, Worktree: input.Worktree, Branch: input.Branch,
		Lease: before.LaunchIntent.Lease, LeaseHolder: before.LaunchIntent.LeaseHolder,
		Project: input.Project, BaseCommit: input.BaseCommit,
	}}
	if !reflect.DeepEqual(expected, after.Snapshot()) {
		t.Fatalf("abandon changed retained resources: %+v", after.Snapshot())
	}
	if len(server.controllers.initialWakeupTasks()) != 0 {
		t.Fatal("abandoned launch remains pending")
	}
	if _, err := os.Stat(input.Worktree); err != nil {
		t.Fatalf("abandon removed worktree: %v", err)
	}
}

func TestHostStopCancelsAbsentLaunchRecovery(t *testing.T) {
	server, input, before := blockedRecoveryFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response := server.stopResponse(ctx, primary.Caller{UID: uint32(os.Getuid())}, Request{
		Operation: HostStop, Status: &StatusRequest{},
	})
	if response.Outcome != Completed {
		t.Fatalf("stop during recovery: %+v", response)
	}
	defer server.shutdownLease.Close()
	after, err := server.store.Read(ctx, input.TaskID)
	if err != nil || !reflect.DeepEqual(before, after.Snapshot()) {
		t.Fatalf("stop changed retained launch: %+v, %v", after.Snapshot(), err)
	}
}

func TestRefusedRecoveryControlResumesLaunch(t *testing.T) {
	for _, operation := range []string{"abandon", "stop"} {
		for _, inventory := range []string{"live", "uncertain"} {
			t.Run(operation+"/"+inventory, func(t *testing.T) {
				server, input, before := blockedRecoveryFixture(t)
				script := "#!/bin/sh\nexit 72\n"
				if inventory == "live" {
					script = `#!/bin/sh
printf '%s\n' '{"result":{"agent":{"pane_id":"p1","agent_status":"working","agent":"codex"}}}'
`
				}
				if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if operation == "abandon" {
					if _, err := server.abandonLaunch(ctx, TransitionRequest{
						TaskID: input.TaskID, Generation: before.LaunchIntent.Generation,
					}); err == nil {
						t.Fatal("abandoned a live or uncertain launch")
					}
				} else {
					response := server.stopResponse(ctx, primary.Caller{UID: uint32(os.Getuid())}, Request{
						Operation: HostStop, Status: &StatusRequest{},
					})
					if response.Outcome != Busy || server.stopping {
						t.Fatalf("stopped a live or uncertain launch: %+v", response)
					}
				}
				after, err := server.store.Read(ctx, input.TaskID)
				if err != nil || !reflect.DeepEqual(before, after.Snapshot()) {
					t.Fatalf("refusal changed launch: %+v, %v", after.Snapshot(), err)
				}
				if len(server.controllers.initialWakeupTasks()) != 1 {
					t.Fatal("refusal lost pending recovery")
				}
				if err := os.WriteFile(server.config.GitExecutable, []byte("#!/bin/sh\nexit 73\n"), 0700); err != nil {
					t.Fatal(err)
				}
				key := controllerKey{kind: taskController, id: input.TaskID}
				if err := server.reconcileInitialTask(ctx, key); err == nil ||
					errors.Is(err, errControllerBusy) {
					t.Fatalf("refused request did not resume launch admission: %v", err)
				}
			})
		}
	}
}
