package hostservice

import (
	"context"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestReopenRefusesUnfinishedOperation(t *testing.T) {
	for _, operation := range []Operation{Stop, Cleanup} {
		t.Run(string(operation), func(t *testing.T) {
			server, key, endpoint := taskEndpointFixture(t)
			_, err := server.store.Update(
				context.Background(),
				"task-1",
				func(task taskstate.Task) (taskstate.Task, error) {
					snapshot := task.Snapshot()
					snapshot.Definition.Kind = "scout"
					task, err := taskstate.NewMigrated(snapshot)
					if err != nil {
						return taskstate.Task{}, err
					}
					return task.AcceptResult("gen-1", taskstate.Close, "2026-10-20")
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			release, err := server.controllers.quiesceOperation(
				context.Background(),
				key,
				endpoint,
				operation,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			_, err = server.perform(
				context.Background(),
				Request{Operation: Reopen, Transition: &TransitionRequest{TaskID: "task-1"}},
			)
			if err == nil ||
				!strings.Contains(err.Error(), string(operation)+" operation is in progress") {
				t.Fatalf("reopen during %s: %v", operation, err)
			}
			current, _ := server.store.Read(context.Background(), "task-1")
			if current.State() != taskstate.Done || len(current.Snapshot().Reopens) != 0 {
				t.Fatal("busy reopen changed task")
			}
		})
	}
}

func TestReopenRequestAndResponse(t *testing.T) {
	request := Request{Operation: Reopen, Transition: &TransitionRequest{TaskID: "task-1"}}
	if err := validateRequest(request); err != nil {
		t.Fatal(err)
	}
	request.Transition.Generation = "gen-1"
	if err := validateRequest(request); err == nil {
		t.Fatal("reopen accepted an unrelated generation")
	}
	request.Transition.Generation = ""
	if err := validateResponse(Response{Outcome: Completed}, request); err == nil {
		t.Fatal("reopen accepted missing result")
	}
}
