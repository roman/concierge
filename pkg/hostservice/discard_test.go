package hostservice

import (
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestDiscardRequiresExplicitOperatorAuthority(t *testing.T) {
	request := Request{
		Operation:  Discard,
		Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
	}
	if err := validateRequest(request); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("missing operator guard: %v", err)
	}
	request.Transition.OperatorApproved = true
	if err := validateRequest(request); err != nil {
		t.Fatal(err)
	}
	request.Operation = Stop
	if err := validateRequest(request); err == nil {
		t.Fatal("discard authority accepted for another operation")
	}
}

func TestDiscardQuiescesExactControllerBeforeFailure(t *testing.T) {
	server, key, endpoint := taskEndpointFixture(t)
	_, err := server.store.Update(
		t.Context(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			worker := snapshot.Worker
			worker.Lease = taskstate.WorktreeLease{
				ID:         "lease",
				Holder:     "holder",
				Project:    worker.Project,
				Path:       worker.Worktree,
				Branch:     "task/work",
				BaseCommit: strings.Repeat("1", 40),
				State:      taskstate.LeaseHeld,
			}
			return taskstate.NewMigrated(snapshot)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan struct{})
	startEndpointWakeup(t, server, key, endpoint, func() error { close(cleaned); return nil })
	response, err := server.perform(
		t.Context(),
		Request{
			Operation: Discard,
			Transition: &TransitionRequest{
				TaskID:           "task-1",
				Generation:       "gen-1",
				OperatorApproved: true,
			},
		},
	)
	if err == nil {
		t.Fatal("unknown absence accepted")
	}
	response.Outcome, response.Error = Failed, err.Error()
	if err := validateResponse(response, Request{Operation: Discard, Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1", OperatorApproved: true}}); err != nil {
		t.Fatalf("discard refusal cannot cross the host boundary: %v", err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("controller remains active")
	}
	task, err := server.store.Read(t.Context(), "task-1")
	if err != nil || task.Snapshot().Worker == nil {
		t.Fatalf("refusal cleared binding: %v", err)
	}
}

func TestLaunchAbandonmentCannotReturnDiscardReceipt(t *testing.T) {
	server, _, _ := taskEndpointFixture(t)
	_, err := server.store.Update(
		t.Context(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			worker := snapshot.Worker
			worker.Lease = taskstate.WorktreeLease{
				ID:         "lease",
				Holder:     "holder",
				Project:    worker.Project,
				Path:       worker.Worktree,
				Branch:     "task/work",
				BaseCommit: strings.Repeat("1", 40),
				State:      taskstate.LeaseHeld,
			}
			current, err := taskstate.NewMigrated(snapshot)
			if err != nil {
				return taskstate.Task{}, err
			}
			discarded, err := current.DiscardWorker("gen-1")
			if err != nil {
				return taskstate.Task{}, err
			}
			return discarded.RecordLeaseReturned("gen-1", worker.Lease)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.perform(
		t.Context(),
		Request{
			Operation:  AbandonLaunch,
			Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
		},
	)
	if err == nil {
		t.Fatal("launch abandonment accepted a worker discard")
	}
}
