package taskops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestRetireStalePrimaryWakesPreservesLiveAndUnknownGenerations(t *testing.T) {
	for _, test := range []struct {
		name        string
		generation  string
		change      func(*taskstate.Snapshot)
		wantRetired bool
	}{
		{name: "live worker", generation: "gen-1"},
		{name: "unknown generation", generation: "unknown"},
		{name: "retired receipt", generation: "gen-old", wantRetired: true},
		{name: "prior receipt", generation: "gen-prior", wantRetired: true},
		{name: "replacement worker", generation: "gen-2", change: func(s *taskstate.Snapshot) {
			s.Worker.Generation = "gen-2"
		}},
		{name: "old wake after relaunch", generation: "gen-old", wantRetired: true, change: func(s *taskstate.Snapshot) {
			s.Worker.Generation = "gen-2"
		}},
		{name: "reserved restart", generation: "gen-next", change: func(s *taskstate.Snapshot) {
			s.Worker.PendingLaunch = "gen-next"
		}},
		{name: "closed with live accepted worker", generation: "gen-1", change: func(s *taskstate.Snapshot) {
			s.State = taskstate.Done
			s.Definition.Closed = "2026-10-22"
			s.Completion = &taskstate.Completion{Generation: "gen-1", Mode: taskstate.Close, Phase: taskstate.Accepted}
		}},
		{name: "closed task without matching receipt", generation: "unknown", wantRetired: true, change: func(s *taskstate.Snapshot) {
			s.Worker = nil
			s.State = taskstate.Done
			s.Definition.Closed = "2026-10-22"
			s.Receipt.Mode = taskstate.Close
		}},
		{name: "restarted generation", generation: "gen-1", wantRetired: true, change: func(s *taskstate.Snapshot) {
			s.Worker.Generation = "gen-2"
			s.Worker.Restarts = []taskstate.RestartPlan{{From: "gen-1", To: "gen-2"}}
		}},
		{name: "retired migrated completion", generation: "gen-1", wantRetired: true, change: func(s *taskstate.Snapshot) {
			s.Worker = nil
			s.Completion = &taskstate.Completion{Generation: "gen-1", Mode: taskstate.Retain, Phase: taskstate.Retired}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, messageRoot := runningWorkerService(t)
			if _, err := service.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
				snapshot := task.Snapshot()
				snapshot.Receipt = &taskstate.Receipt{Generation: "gen-old", Mode: taskstate.Retain}
				snapshot.PriorReceipts = []taskstate.Receipt{{Generation: "gen-prior", Mode: taskstate.Retain}}
				if test.change != nil {
					test.change(&snapshot)
				}
				return taskstate.NewMigrated(snapshot)
			}); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(service.root, "records", "task-1.json")
			before, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			messages, _ := inbox.New(messageRoot)
			wake, err := messages.Send(
				service.primaryRecipient,
				fmt.Sprintf(
					"task-restart:%s:task-1:%s",
					service.root,
					test.generation,
				),
				"diagnostic",
			)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.RetireStalePrimaryWakes(t.Context(), messageRoot); err != nil {
					t.Fatal(err)
				}
				pending, err := messages.List(service.primaryRecipient)
				if err != nil || (len(pending) == 0) != test.wantRetired {
					t.Fatalf("pending = %v, error = %v", pending, err)
				}
			}
			if test.wantRetired {
				if _, err := messages.ReadHandled(service.primaryRecipient, wake.ID); err != nil {
					t.Fatal(err)
				}
			}
			after, err := os.ReadFile(record)
			if err != nil || string(before) != string(after) {
				t.Fatalf("sweep changed task record: %v", err)
			}
		})
	}
}

func TestRetireStalePrimaryWakesLeavesUnrecognizedMessagesPending(t *testing.T) {
	service, _, messageRoot := runningWorkerService(t)
	if _, err := service.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Worker = nil
		snapshot.State = taskstate.Done
		snapshot.Definition.Closed = "2026-10-22"
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(messageRoot)
	for _, key := range []string{
		"ordinary-message",
		fmt.Sprintf("task-reply:%s:task-1:gen-1", service.root),
		fmt.Sprintf("task-restart:%s:missing:gen-1", service.root),
		fmt.Sprintf("task-restart:%s:task-1:", service.root),
		fmt.Sprintf("task-restart:%s:task-1:gen-1:extra", service.root),
		fmt.Sprintf("task-stalled:%s:task-1:gen-1", service.root),
		fmt.Sprintf("task-native-checkpoint:%s:task-1:gen-1:", service.root),
		fmt.Sprintf("task-restart:%s:../task-1:gen-1", service.root),
		fmt.Sprintf("task-restart:%s:task-1:bad generation", service.root),
		fmt.Sprintf("task-restart:%s:%s:gen-1", service.root, strings.Repeat("a", 201)),
		fmt.Sprintf("task-restart:%s-other:task-1:gen-1", service.root),
	} {
		if _, err := messages.Send(service.primaryRecipient, key, "task is closed"); err != nil {
			t.Fatal(err)
		}
	}
	before, err := messages.List(service.primaryRecipient)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RetireStalePrimaryWakes(t.Context(), messageRoot); err != nil {
		t.Fatal(err)
	}
	after, err := messages.List(service.primaryRecipient)
	if err != nil || len(after) != len(before) {
		t.Fatalf("unrecognized messages changed: %v, %v", after, err)
	}
}
