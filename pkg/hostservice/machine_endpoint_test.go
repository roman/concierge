package hostservice

import (
	"context"
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestControllerReservationsScopePaneByMachine(t *testing.T) {
	registry := newControllerRegistry(context.Background())
	defer registry.close()
	first := controllerEndpoint{machine: "machine-1", session: "work", pane: "pane-1"}
	second := controllerEndpoint{machine: "machine-2", session: "work", pane: "pane-1"}
	releaseFirst, err := registry.reserveAdoption(
		controllerKey{kind: taskController, id: "task-1"},
		first,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	releaseSecond, err := registry.reserveAdoption(
		controllerKey{kind: taskController, id: "task-2"},
		second,
	)
	if err != nil {
		t.Fatalf("independent machine collided: %v", err)
	}
	defer releaseSecond()
	if release, err := registry.reserveAdoption(controllerKey{kind: taskController, id: "task-3"}, first); !errors.Is(
		err,
		errControllerBusy,
	) {
		if release != nil {
			release()
		}
		t.Fatalf("duplicate endpoint was accepted: %v", err)
	}
}

func TestTaskStopEndpointKeepsMachine(t *testing.T) {
	server, _, _ := taskEndpointFixture(t)
	task, err := server.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := task.Snapshot()
	snapshot.Worker.Machine = "machine-1"
	task, err = taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := taskOperationEndpoint(task, Stop, "gen-1")
	if err != nil || endpoint == nil || endpoint.machine != "machine-1" {
		t.Fatalf("stop endpoint=%+v error=%v", endpoint, err)
	}
}
