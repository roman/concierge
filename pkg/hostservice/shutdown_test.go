package hostservice

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestHostStopRefusesActiveWork(t *testing.T) {
	for _, activity := range []string{"launch", "landing"} {
		t.Run(activity, func(t *testing.T) {
			server, root, socket := fixture(t, nil)
			lease, err := hostactivity.Begin(root)
			if err != nil {
				t.Fatal(err)
			}
			response, err := Call(
				context.Background(),
				socket,
				Request{Operation: HostStop, Status: &StatusRequest{}},
			)
			lease.Close()
			if err != nil || response.Outcome != Busy ||
				!strings.Contains(response.Error, "in progress") {
				t.Fatalf("stop during %s: %+v, %v", activity, response, err)
			}
			server.shutdownMu.Lock()
			stopping := server.stopping
			server.shutdownMu.Unlock()
			if stopping {
				t.Fatal("refused stop changed host lifetime")
			}
		})
	}
}

func TestHostStopRefusesPendingLaunch(t *testing.T) {
	server, _, socket := fixture(t, nil)
	ctx := context.Background()
	if _, err := server.service.Create(ctx, *intakeRequest().Intake); err != nil {
		t.Fatal(err)
	}
	_, err := server.store.ReserveInitialLaunch(ctx, "task-1", taskstate.LaunchIntent{
		Generation: "generation", Name: "worker", Label: "worker", Harness: "codex", Session: "work",
		Project: "/project", Worktree: "/worktree", Branch: "task", BaseCommit: strings.Repeat("a", 40), Body: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := Call(ctx, socket, Request{Operation: HostStop, Status: &StatusRequest{}})
	if err != nil || response.Outcome != Busy || !strings.Contains(response.Error, "task-1") {
		t.Fatalf("pending launch: %+v, %v", response, err)
	}
}

func TestHostStopLeavesTaskRecords(t *testing.T) {
	server, _, socket := fixture(t, nil)
	ctx := context.Background()
	if _, err := server.service.Create(ctx, *intakeRequest().Intake); err != nil {
		t.Fatal(err)
	}
	response, err := Call(ctx, socket, Request{Operation: HostStop, Status: &StatusRequest{}})
	if err != nil || response.Outcome != Completed || response.Status == nil {
		t.Fatalf("stop: %+v, %v", response, err)
	}
	if _, err := server.store.Read(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestHostStopRejectsForeignUID(t *testing.T) {
	server, _, _ := fixture(t, nil)
	response := server.stopResponse(
		context.Background(),
		primary.Caller{UID: uint32(os.Geteuid()) + 1},
		Request{},
	)
	if response.Outcome != Unauthorized || response.Status != nil {
		t.Fatalf("foreign user stopped the host: %+v", response)
	}
}

func TestHostShutdownJoinsControllersAndPreservesWorker(t *testing.T) {
	server, key, endpoint := taskEndpointFixture(t)
	path := filepath.Join(server.config.Root, "records", key.id+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	startEndpointWakeup(t, server, key, endpoint, func() error { cleaned = true; return nil })
	response := server.stopResponse(
		context.Background(),
		primary.Caller{UID: uint32(os.Geteuid())},
		Request{},
	)
	if response.Outcome != Completed {
		t.Fatalf("shutdown: %+v", response)
	}
	defer server.shutdownLease.Close()
	if err := server.controllers.close(); err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("shutdown did not join the controller")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("shutdown changed the worker: %v", err)
	}
}
