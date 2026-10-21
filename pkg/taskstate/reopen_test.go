package taskstate

import (
	"testing"
	"time"
)

func TestReopenPreservesAcceptanceHistory(t *testing.T) {
	snapshot := Snapshot{
		Definition: Definition{ID: "task-1", Title: "Scout", Repo: "exmachina", Kind: "scout"},
		State:      InFlight,
	}
	worker := workerForTransition()
	worker.Workspace, worker.Tab = "workspace-1", "tab-1"
	snapshot.Worker = &worker
	task, err := NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		task, err = task.AcceptResult("gen-1", Close, "2026-10-20")
		if err != nil {
			t.Fatal(err)
		}
		accepted := task
		task, err = task.Reopen(time.Date(2026, 10, 21, 5, index, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := task.Encode()
		task, err = Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		history := task.Snapshot().Reopens
		if len(history) != index+1 ||
			history[index].Completion != *accepted.Snapshot().Completion ||
			history[index].Closed != "2026-10-20" ||
			history[index].At == "" ||
			history[index].Reason == "" {
			t.Fatalf("lost acceptance: %+v", history)
		}
		history[index].Reason = "changed"
		if task.Snapshot().Reopens[index].Reason == "changed" {
			t.Fatal("history shares mutable storage")
		}
		retried, err := task.Reopen(time.Now())
		if err != nil || len(retried.Snapshot().Reopens) != len(history) {
			t.Fatalf("duplicate reopen: %v", err)
		}
	}
	for _, trial := range []struct {
		name   string
		change func(*ReopenRecord)
	}{
		{"missing reason", func(record *ReopenRecord) { record.Reason = "" }},
		{"invalid timestamp", func(record *ReopenRecord) { record.At = "invalid" }},
		{"invalid acceptance", func(record *ReopenRecord) { record.Completion.Generation = "" }},
		{"invalid closing date", func(record *ReopenRecord) { record.Closed = "invalid" }},
	} {
		t.Run(trial.name, func(t *testing.T) {
			snapshot := task.Snapshot()
			trial.change(&snapshot.Reopens[0])
			if _, err := NewMigrated(snapshot); err == nil {
				t.Fatal("invalid reopen history accepted")
			}
		})
	}
}
