package taskstate

import (
	"strings"
	"testing"
	"time"
)

func TestRemoteConnectionsKeepGenerationAndIndependentRetryState(t *testing.T) {
	snapshot := taskForTransition(t).Snapshot()
	worker := workerForTransition()
	worker.Machine, worker.Workspace, worker.Tab = "machine-1", "workspace-1", "tab-1"
	worker.Lease = WorktreeLease{
		ID:         "lease-1",
		Holder:     "holder",
		Project:    worker.Project,
		Path:       worker.Worktree,
		Branch:     "task/work",
		BaseCommit: strings.Repeat("a", 40),
		State:      LeaseHeld,
	}
	snapshot.State, snapshot.Worker = InFlight, &worker
	task, err := NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for attempt := 1; attempt <= 10; attempt++ {
		task, err = task.RecordConnection(
			"gen-1",
			HerdrChannel,
			ConnectionObservation{At: now, Error: "SSH unavailable"},
		)
		if err != nil {
			t.Fatal(err)
		}
		bound := task.Snapshot().Worker
		state := bound.Connections.Herdr
		if state.Status != ConnectionUnreachable || state.NextRetry.Sub(now) > time.Minute ||
			!state.Waiting(now) ||
			bound.Generation != "gen-1" ||
			bound.Worktree != worker.Worktree ||
			bound.Lease != worker.Lease {
			t.Fatalf("unreachable binding changed: %+v", bound)
		}
		now = state.NextRetry
	}
	before := task.Snapshot().Worker.Connections.Herdr
	task, err = task.RecordConnection(
		"gen-1",
		TunnelChannel,
		ConnectionObservation{At: now, Error: "tunnel unavailable"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Snapshot().Worker.Connections.Herdr != before {
		t.Fatal("tunnel probe changed Herdr retry state")
	}
	task, err = task.RecordConnection("gen-1", HerdrChannel, ConnectionObservation{At: now})
	if err != nil {
		t.Fatal(err)
	}
	state := task.Snapshot().Worker.Connections.Herdr
	if state.Status != ConnectionReachable || state.LastSuccess != now || state.Failures != 0 ||
		!state.NextRetry.IsZero() {
		t.Fatalf("recovery did not reset retry state: %+v", state)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || *decoded.Snapshot().Worker.Connections != *task.Snapshot().Worker.Connections {
		t.Fatalf("connection state did not survive reload: %v", err)
	}
	copy := task.Snapshot()
	copy.Worker.Connections.Herdr.LastError = "external mutation"
	if task.Snapshot().Worker.Connections.Herdr.LastError != "" {
		t.Fatal("snapshot shared mutable connection state")
	}
	if _, err := task.RecordConnection("other-generation", HerdrChannel, ConnectionObservation{At: now}); err == nil {
		t.Fatal("stale generation changed connectivity")
	}
	reserved, err := task.ReserveLaunch("gen-2")
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := reserved.AdvanceGeneration("gen-2")
	if err != nil || advanced.Snapshot().Worker.Connections != nil {
		t.Fatalf("new generation retained old retry state: %+v %v", advanced.Snapshot().Worker, err)
	}
}
