package taskops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteRetirementReturnsBothLeasesAndRecoversLostPrimaryConfirmation(t *testing.T) {
	for _, mode := range []string{"complete", "lost primary return"} {
		t.Run(mode, func(t *testing.T) {
			service, _, directory := remoteResultMaterializationFixture(t)
			ctx := context.Background()
			ready, err := service.MaterializeRemoteResult(ctx, "task-1", "gen-1")
			if err != nil {
				t.Fatal(err)
			}
			port := &remoteLeasePortFixture{returned: ready.Snapshot().Worker.Lease}
			port.returned.State = taskstate.LeaseReturned
			service = service.WithRemoteLeases(port)
			if _, err := service.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
				return task.AcceptResult("gen-1", taskstate.Close, "2026-10-17")
			}); err != nil {
				t.Fatal(err)
			}
			retire := func() (taskstate.Task, error) {
				return service.store.Update(
					ctx,
					"task-1",
					func(task taskstate.Task) (taskstate.Task, error) {
						retired, err := task.RetireResult("gen-1")
						if err != nil {
							return taskstate.Task{}, err
						}
						return service.returnRetiredLease(ctx, retired, "gen-1")
					},
				)
			}
			if mode == "lost primary return" {
				if err := os.WriteFile(filepath.Join(directory, "lose-return"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := retire(); err == nil {
					t.Fatal("confirmed a lost primary lease return")
				}
				pending, err := service.store.Read(ctx, "task-1")
				if err != nil || pending.Snapshot().Worker == nil ||
					pending.Snapshot().Completion == nil ||
					pending.Snapshot().Worker.RemoteResult.Lease.State != taskstate.LeaseHeld {
					t.Fatal("uncertain primary return discarded the worker binding")
				}
			}
			retired, err := retire()
			if err != nil {
				t.Fatal(err)
			}
			native, _ := retired.RetiredLease("gen-1")
			primary, found := retired.RetiredRemoteResult("gen-1")
			if !found || native.State != taskstate.LeaseReturned ||
				primary.Lease.State != taskstate.LeaseReturned {
				t.Fatal("cleanup did not confirm both leases")
			}
			returns := port.returnCalls
			if _, err := retire(); err != nil || port.returnCalls != returns {
				t.Fatal("repeated a confirmed native return")
			}
			if data, err := os.ReadFile(filepath.Join(directory, "returns")); err != nil ||
				string(data) != "return\n" {
				t.Fatal("repeated or omitted the primary return")
			}
		})
	}
}
