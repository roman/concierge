package taskstate

import (
	"context"
	"fmt"
	"time"
)

// RelocateReopenedWorker checks every task's resource claims before moving a binding.
func (store *Store) RelocateReopenedWorker(
	ctx context.Context,
	taskID, generation, previousPane string,
	endpoint LaunchEndpoint,
) (Task, error) {
	return store.reserveWorkerResources(ctx, taskID, func(task Task) (Task, error) {
		bound := task.Snapshot().Worker
		if bound == nil || bound.Generation != generation || bound.Pane != previousPane {
			return Task{}, ErrInvalidTransition
		}
		return task.RelocateReopenedWorker(endpoint)
	})
}

// ReopenRecord preserves the superseded acceptance and any pane recovery request.
type ReopenRecord struct {
	Completion    Completion `json:"completion"`
	Closed        string     `json:"closed,omitempty"`
	At            string     `json:"at"`
	Reason        string     `json:"reason"`
	PaneRequested bool       `json:"pane_requested,omitempty"`
}

func validReopens(history []ReopenRecord) bool {
	for _, record := range history {
		if !validCompletion(record.Completion) || record.Completion.Phase != Accepted ||
			!singleLine(record.Reason) || record.Reason == "" || !validDate(record.Closed) {
			return false
		}
		at, err := time.Parse(time.RFC3339Nano, record.At)
		if err != nil || at.IsZero() {
			return false
		}
		if record.Completion.Mode == Close && record.Closed == "" {
			return false
		}
		if record.Completion.Mode == Retain && record.Closed != "" {
			return false
		}
	}
	return true
}

// CheckReopen restricts follow-up work to scouts whose binding is still retained.
func (task Task) CheckReopen() error {
	snapshot := task.Snapshot()
	if snapshot.Definition.Kind != "scout" ||
		(snapshot.Worker != nil && snapshot.Worker.Machine != "") {
		return fmt.Errorf("reopen supports only local scouts; create a new task for project work")
	}
	if snapshot.Worker == nil {
		return fmt.Errorf(
			"reopen requires a retained worker binding; cleanup has retired this worker",
		)
	}
	if snapshot.LaunchIntent != nil || snapshot.Worker.PendingAdoption ||
		snapshot.Definition.Hold != nil ||
		snapshot.Closure != nil {
		return ErrInvalidTransition
	}
	if snapshot.State == InFlight && snapshot.Completion == nil && len(snapshot.Reopens) > 0 {
		return nil
	}
	if snapshot.Completion == nil || snapshot.Completion.Phase != Accepted ||
		(snapshot.State != Done && snapshot.State != Queued) {
		return ErrInvalidTransition
	}
	return nil
}

// Reopen supersedes acceptance without changing the retained worker generation.
func (task Task) Reopen(at time.Time) (Task, error) {
	if err := task.CheckReopen(); err != nil {
		return Task{}, err
	}
	snapshot := task.Snapshot()
	if snapshot.State == InFlight {
		return task, nil
	}
	snapshot.Reopens = append(snapshot.Reopens, ReopenRecord{
		Completion: *snapshot.Completion, Closed: snapshot.Definition.Closed,
		At: at.UTC().Format(time.RFC3339Nano), Reason: "operator requested follow-up",
	})
	snapshot.State = InFlight
	snapshot.Definition.Closed = ""
	snapshot.Completion = nil
	return newTransition(snapshot)
}

// RequestReopenPane records creation before submitting the external operation.
func (task Task) RequestReopenPane() (Task, error) {
	if err := task.CheckReopen(); err != nil {
		return Task{}, err
	}
	snapshot := task.Snapshot()
	if snapshot.State != InFlight || len(snapshot.Reopens) == 0 {
		return Task{}, ErrInvalidTransition
	}
	snapshot.Reopens[len(snapshot.Reopens)-1].PaneRequested = true
	return newTransition(snapshot)
}

// RelocateReopenedWorker records a replacement pane before supervised restart.
func (task Task) RelocateReopenedWorker(endpoint LaunchEndpoint) (Task, error) {
	if err := task.CheckReopen(); err != nil {
		return Task{}, err
	}
	snapshot := task.Snapshot()
	if snapshot.State != InFlight || len(snapshot.Reopens) == 0 ||
		!snapshot.Reopens[len(snapshot.Reopens)-1].PaneRequested {
		return Task{}, ErrInvalidTransition
	}
	snapshot.Worker.Workspace = endpoint.Workspace
	snapshot.Worker.Tab = endpoint.Tab
	snapshot.Worker.Pane = endpoint.Pane
	return newTransition(snapshot)
}
