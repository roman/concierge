package hostservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func TestOrphanRecoveryRequiresApprovalBeforeAdmission(t *testing.T) {
	request := Request{Operation: RecoverOrphaned, Transition: &TransitionRequest{
		TaskID: "task-1", Generation: "gen-1", Reason: "Superseded", ClosedOn: "2026-10-20",
	}}
	if err := validateRequest(request); err == nil {
		t.Fatal("recovery accepted missing operator approval")
	}
	request.Transition.OperatorApproved = true
	if err := validateRequest(request); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanRecoveryInvalidReasonLeavesControllerActive(t *testing.T) {
	for _, reason := range []string{"Has\ta tab", "Has\x1bescape", "Has\u0085control"} {
		t.Run(reason, func(t *testing.T) {
			server, root, socket := fixture(t, func(server *Server) {
				server.config.Recovery = RecoveryMaintenance
				server.authorize = func(primary.Caller) error { return nil }
			})
			worker := &taskstate.Worker{Name: "worker", Harness: "codex", Session: "work",
				Workspace: "workspace-1", Tab: "tab-1", Pane: "pane-1", Project: "/repo", Worktree: "/worktree",
				Generation: "gen-1", PendingLaunch: "gen-2", AgentArgs: []string{"resume", "thread"},
				Lease: taskstate.WorktreeLease{
					ID:         "lease",
					Holder:     "holder",
					Project:    "/repo",
					Path:       "/worktree",
					Branch:     "task/work",
					BaseCommit: strings.Repeat("a", 40),
					State:      taskstate.LeaseHeld,
				}}
			task, err := taskstate.NewMigrated(
				taskstate.Snapshot{Definition: *intakeRequest().Intake,
					State: taskstate.InFlight, Worker: worker},
			)
			var data []byte
			if err == nil {
				data, err = task.Encode()
			}
			if err == nil {
				err = os.WriteFile(filepath.Join(root, "records", "task-1.json"), data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			cleaned := make(chan struct{})
			startEndpointWakeup(
				t,
				server,
				controllerKey{kind: taskController, id: "task-1"},
				controllerEndpoint{
					session: "work",
					pane:    "pane-1",
				},
				func() error { close(cleaned); return nil },
			)
			response, err := Call(
				t.Context(),
				socket,
				Request{Operation: RecoverOrphaned, Transition: &TransitionRequest{
					TaskID: "task-1", Generation: "gen-1", Reason: reason, ClosedOn: "2026-10-20", OperatorApproved: true,
				}},
			)
			select {
			case <-cleaned:
				t.Error("invalid recovery reason stopped the controller")
			default:
			}
			if err != nil || response.Outcome != Invalid {
				t.Fatalf("invalid reason passed admission: %+v %v", response, err)
			}
		})
	}
}

func TestOrphanRecoveryQuiescesControllerAndUnblocksHostStop(t *testing.T) {
	server, key, endpoint := taskEndpointFixture(t)
	lease := taskstate.WorktreeLease{
		ID:         "lease",
		Holder:     "holder",
		Project:    t.TempDir(),
		Path:       "/worktree",
		Branch:     "task/work",
		BaseCommit: strings.Repeat("a", 40),
		State:      taskstate.LeaseHeld,
	}
	_, err := server.store.Update(
		t.Context(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			snapshot.Worker.Lease = lease
			snapshot.Worker.Project = lease.Project
			snapshot.Worker.PendingLaunch = "gen-2"
			return taskstate.NewMigrated(snapshot)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1 $2" in
'pane get') printf '%s\n' '{"error":{"code":"pane_not_found"}}'; exit 1 ;;
'agent get') printf '%s\n' '{"error":{"code":"agent_not_found"}}'; exit 1 ;;
'workspace list') printf '%s\n' '{"result":{"workspaces":[]}}' ;;
*) exit 72 ;;
esac
`
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(t.TempDir(), "treehouse")
	if err := os.WriteFile(pool, []byte("#!/bin/sh\n[ \"$1\" = status ] || exit 72\nprintf '%s\\n' '[{\"path\":\"/worktree\",\"lease_id\":\"lease\",\"lease_holder\":\"holder\",\"flavor\":\"git\",\"processes\":[]}]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	server.service, err = taskops.NewWithWorkerTools(
		server.config.Root,
		server.config.Recipient,
		taskops.WorkerTools{
			Commands: server.config.WorkerCommands, Pool: worktree.PoolClient{Executable: pool},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan struct{})
	startEndpointWakeup(t, server, key, endpoint, func() error { close(cleaned); return nil })
	request := Request{Operation: RecoverOrphaned, Transition: &TransitionRequest{
		TaskID: "task-1", Generation: "gen-1", Reason: "Superseded", ClosedOn: "2026-10-20", OperatorApproved: true,
	}}
	response, err := server.perform(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("recovery did not stop the exact controller")
	}
	response.Outcome = Completed
	if err := validateResponse(response, request); err != nil {
		t.Fatal(err)
	}
	request.Transition.Reason = "Changed decision"
	if err := validateResponse(response, request); err == nil {
		t.Fatal("accepted a different closure reason in the host response")
	}
	current, err := server.store.Read(t.Context(), "task-1")
	if err != nil || current.Snapshot().Closure.Worker.PendingLaunch != "gen-2" {
		t.Fatalf("recovery lost pending launch identity: %v", err)
	}
	if err := server.restoreTaskControllers(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(server.controllers.initialWakeupTasks()) != 0 {
		t.Fatal("host restart resumed an archived launch")
	}
	stopped := server.stopResponse(
		t.Context(),
		primary.Caller{UID: uint32(os.Getuid())},
		Request{Operation: HostStop, Status: &StatusRequest{}},
	)
	if stopped.Outcome != Completed {
		t.Fatalf("recovery left host stop blocked: %+v", stopped)
	}
	if server.shutdownLease != nil {
		server.shutdownLease.Close()
	}
}
