package hostservice

import (
	"context"
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestTaskStopRejectsChangedRemoteTargetBeforeQuiescing(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	ctx := context.Background()
	fixture.server.controllers = newControllerRegistry(ctx)
	if _, err := fixture.server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		return task.CompleteInitialLaunch("gen-1")
	}); err != nil {
		t.Fatal(err)
	}
	profile := fixture.profile
	profile.Target = "another-target"
	fixture.server.preparationMachines = launchLocationCatalog{profile}
	executed := false
	_, err := fixture.server.withTaskEndpoint(ctx, Request{
		Operation:  Stop,
		Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
	}, func() (Response, error) {
		executed = true
		return Response{}, nil
	})
	if !errors.Is(err, remoteworker.ErrWorktreeOwnership) || executed {
		t.Fatalf("changed remote target reached task stop: executed=%v err=%v", executed, err)
	}
	current, err := fixture.server.store.Read(ctx, "task-1")
	if err != nil || current.Snapshot().Worker.Generation != "gen-1" ||
		current.Snapshot().Completion != nil {
		t.Fatal("changed remote target altered the worker binding")
	}
}
