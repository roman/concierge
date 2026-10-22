package hostservice

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestCloseOrphanQuiescesControllerAndValidatesResponse(t *testing.T) {
	server, key, endpoint := taskEndpointFixture(t)
	_, err := server.store.Update(
		t.Context(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			snapshot.Worker.Lease = taskstate.WorktreeLease{
				ID: "lease", Holder: "holder", Project: "/repo", Path: "/worktree",
				Branch: "task/work", BaseCommit: strings.Repeat("a", 40), State: taskstate.LeaseHeld,
			}
			return taskstate.NewMigrated(snapshot)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(
		"#!/bin/sh\nprintf '%s\\n' '{\"error\":{\"code\":\"pane_not_found\"}}'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan struct{})
	startEndpointWakeup(t, server, key, endpoint, func() error { close(cleaned); return nil })
	request := Request{Operation: CloseRetired, Transition: &TransitionRequest{
		TaskID: "task-1", Generation: "gen-1", Reason: "Superseded", ClosedOn: "2026-10-21",
	}}
	messages, _ := inbox.New(server.config.MessageRoot)
	wake, err := messages.Send(server.config.Recipient,
		fmt.Sprintf("task-restart:%s:task-1:gen-1", server.config.Root), "diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response, err := server.perform(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-cleaned:
		default:
			t.Fatal("closure did not join the controller")
		}
		response.Outcome = Completed
		if err := validateResponse(response, request); err != nil {
			t.Fatal(err)
		}
		if _, err := messages.ReadHandled(server.config.Recipient, wake.ID); err != nil {
			t.Fatalf("closure left wake pending: %v", err)
		}
	}
}

func TestCloseOrphanHistoricalGenerationKeepsCurrentController(t *testing.T) {
	server, key, endpoint := taskEndpointFixture(t)
	_, err := server.store.Update(
		t.Context(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			lease := taskstate.WorktreeLease{
				ID:         "old-lease",
				Holder:     "old-holder",
				Project:    "/repo",
				Path:       "/old-worktree",
				Branch:     "task/old",
				BaseCommit: strings.Repeat("a", 40),
				State:      taskstate.LeaseReturned,
			}
			snapshot.AbandonedLaunches = []taskstate.LaunchAbandonment{{
				Kind: taskstate.DiscardedWorker, WorkerRetired: true, Generation: "old-gen",
				Lease: lease, LeaseHolder: lease.Holder, Project: lease.Project,
				Worktree: lease.Path, Branch: lease.Branch, BaseCommit: lease.BaseCommit,
			}}
			return taskstate.NewMigrated(snapshot)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan struct{})
	startEndpointWakeup(t, server, key, endpoint, func() error { close(cleaned); return nil })
	_, err = server.perform(
		t.Context(),
		Request{Operation: CloseRetired, Transition: &TransitionRequest{
			TaskID: "task-1", Generation: "old-gen", Reason: "Superseded", ClosedOn: "2026-10-21",
		}},
	)
	if err == nil {
		t.Fatal("closed a historical generation")
	}
	select {
	case <-cleaned:
		t.Fatal("historical close stopped current supervision")
	default:
	}
}
