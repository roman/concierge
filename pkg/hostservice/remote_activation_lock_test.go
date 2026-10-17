package hostservice

import (
	"context"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestHostRemoteActivationUsesTheExistingTaskLock(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	tools, err := fixture.request.Tools()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := fixture.server.store.WithTask(
		ctx,
		"task-1",
		func(locked *taskstate.LockedTask) error {
			return fixture.server.activateRemoteWorkerLocked(
				ctx,
				locked,
				"gen-1",
				fixture.profile,
				tools,
			)
		},
	)
	if err != nil || task.Snapshot().Worker.TransportGeneration != "gen-1" {
		t.Fatalf("activation under the existing task lock: %v", err)
	}
}
