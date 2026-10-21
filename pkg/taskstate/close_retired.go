package taskstate

import (
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
)

func validClosure(selected record) bool {
	closure := selected.Closure
	if closure == nil {
		return true
	}
	if selected.State != Done || selected.Closed == "" || selected.Worker != nil ||
		selected.LaunchIntent != nil || selected.Completion != nil ||
		!singleLine(closure.Reason) || strings.TrimSpace(closure.Reason) == "" ||
		taskmeta.ValidateSpawnGeneration(closure.Generation) != nil {
		return false
	}
	if closure.Worker != nil {
		return validOrphanedClosure(selected)
	}
	if selected.Hold == nil {
		return false
	}
	if selected.Receipt != nil && selected.Receipt.Generation == closure.Generation &&
		selected.Receipt.Mode == Retain {
		return true
	}
	_, found := (Task{record: selected}).LaunchAbandonment(closure.Generation)
	return found
}

// CloseRetired closes held retired work or a confirmed absent local worker.
// Existing receipts, holds and lease reservations remain available for inspection.
// The caller must exclude worker execution and pending replies before closing a binding.
func (task Task) CloseRetired(generation, reason, closedOn string) (Task, error) {
	closure := Closure{Generation: generation, Reason: reason}
	if !singleLine(reason) || strings.TrimSpace(reason) == "" || closedOn == "" ||
		!validDate(closedOn) {
		return Task{}, ErrInvalidTransition
	}
	if task.record.Closure != nil {
		if task.record.Closure.Generation == generation && task.record.Closure.Reason == reason &&
			task.record.Closed == closedOn {
			return task, nil
		}
		return Task{}, ErrInvalidTransition
	}
	if task.record.Worker != nil {
		if err := task.CheckDiscardWorker(generation); err != nil {
			return Task{}, err
		}
		return task.closeOrphanedWorker(generation, reason, closedOn)
	}
	if task.State() != Queued || task.record.Hold == nil || task.record.Worker != nil ||
		task.record.LaunchIntent != nil || task.record.Completion != nil {
		return Task{}, ErrInvalidTransition
	}
	retained := task.record.Receipt != nil && task.record.Receipt.Generation == generation &&
		task.record.Receipt.Mode == Retain
	_, abandoned := task.LaunchAbandonment(generation)
	if !retained && !abandoned {
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	snapshot.State = Done
	snapshot.Definition.Closed = closedOn
	snapshot.Closure = &closure
	return newTransition(snapshot)
}
