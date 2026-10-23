package taskops

import (
	"context"
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

type remoteLeasePortFixture struct {
	returned     taskstate.WorktreeLease
	inspected    taskstate.WorktreeLease
	returnCalls  int
	inspectCalls int
}

func (port *remoteLeasePortFixture) Return(
	_ context.Context,
	task taskstate.Task,
	generation string,
	lease taskstate.WorktreeLease,
	_ func(string),
) (taskstate.WorktreeLease, error) {
	port.returnCalls++
	current, found := task.RetiredLease(generation)
	if !found || current != lease {
		return taskstate.WorktreeLease{}, errors.New("unbound return request")
	}
	return port.returned, nil
}

func (port *remoteLeasePortFixture) InspectAbandoned(
	context.Context,
	string,
	taskstate.LaunchAbandonment,
) (taskstate.WorktreeLease, error) {
	port.inspectCalls++
	if port.inspected == (taskstate.WorktreeLease{}) {
		return taskstate.WorktreeLease{}, errors.New("unexpected acquisition inspection")
	}
	return port.inspected, nil
}

func TestRemoteLeasePortRecoversOnlyTheAbandonedAcquisition(t *testing.T) {
	for _, mode := range []string{"confirmed", "foreign target"} {
		t.Run(mode, func(t *testing.T) {
			service, intent, _, _ := remoteInitialLeaseFixture(t)
			ctx := context.Background()
			if _, err := service.store.RequestLaunchLease(ctx, "task-1", intent.Generation, "remote-holder"); err != nil {
				t.Fatal(err)
			}
			original, err := service.store.Update(
				ctx,
				"task-1",
				func(task taskstate.Task) (taskstate.Task, error) {
					return task.AbandonInitialLaunch(intent.Generation)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			location := intent.Remote
			location.Backend = taskstate.RemoteGit
			lease := taskstate.WorktreeLease{
				Remote: location, ID: "captured-native-lease", Holder: "remote-holder",
				Project: location.ProjectPath(), Path: location.PoolRoot() + "/captured",
				Branch: intent.Branch, BaseCommit: intent.BaseCommit, State: taskstate.LeaseHeld,
			}
			if mode == "foreign target" {
				lease.Remote.Target = "different-target"
			}
			port := &remoteLeasePortFixture{inspected: lease, returned: lease}
			port.returned.State = taskstate.LeaseReturned
			configured := service.WithRemoteLeases(port)
			result, err := configured.ReleaseWorktree(ctx, "task-1", intent.Generation)
			if port.inspectCalls != 1 {
				t.Fatal("did not inspect the original native acquisition")
			}
			if mode == "foreign target" {
				if err == nil || port.returnCalls != 0 {
					t.Fatal("accepted a foreign abandoned lease")
				}
				current, err := service.store.Read(ctx, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				before, _ := original.LaunchAbandonment(intent.Generation)
				after, _ := current.LaunchAbandonment(intent.Generation)
				if before != after {
					t.Fatal("foreign capture changed the abandoned receipt")
				}
				return
			}
			if err != nil || port.returnCalls != 1 {
				t.Fatalf("native abandoned release: %v", err)
			}
			receipt, _ := result.LaunchAbandonment(intent.Generation)
			if receipt.Lease != port.returned {
				t.Fatal("lost the confirmed native lease")
			}
			if _, err := configured.ReleaseWorktree(ctx, "task-1", intent.Generation); err != nil ||
				port.inspectCalls != 1 ||
				port.returnCalls != 1 {
				t.Fatalf("repeated a confirmed abandoned release: %v", err)
			}
		})
	}
}

func TestRemoteLeasePortRequiresExactConfirmedReturn(t *testing.T) {
	service, retired := remoteRetiredLeaseFixture(t)
	receipt := retired.Snapshot().Receipt
	for _, mode := range []string{"confirmed", "held", "foreign holder", "foreign machine"} {
		t.Run(mode, func(t *testing.T) {
			response := receipt.Lease
			response.State = taskstate.LeaseReturned
			switch mode {
			case "held":
				response.State = taskstate.LeaseHeld
			case "foreign holder":
				response.Holder = "different-holder"
			case "foreign machine":
				response.Remote.Machine = "different-machine"
			}
			port := &remoteLeasePortFixture{returned: response}
			configured := service.WithRemoteLeases(port)
			result, err := configured.returnRetiredLease(
				context.Background(),
				retired,
				receipt.Generation,
			)
			if port.returnCalls != 1 || port.inspectCalls != 0 {
				t.Fatal("used the wrong native operation")
			}
			if mode != "confirmed" {
				if err == nil {
					t.Fatal("recorded an unconfirmed remote release")
				}
				return
			}
			if err != nil || result.Snapshot().Receipt.Lease != response {
				t.Fatalf("confirmed release: %+v %v", result.Snapshot(), err)
			}
			if _, err := configured.returnRetiredLease(context.Background(), result, receipt.Generation); err != nil ||
				port.returnCalls != 1 {
				t.Fatalf("confirmed release repeated an external operation: %v", err)
			}
		})
	}
}
