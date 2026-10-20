package taskstate

import "github.com/roman/concierge/pkg/taskmeta"

// CheckDiscardWorker excludes results, replacements and unfinished launches.
func (task Task) CheckDiscardWorker(generation string) error {
	if receipt, found := task.LaunchAbandonment(generation); found {
		if receipt.Kind == DiscardedWorker {
			return nil
		}
		return ErrInvalidTransition
	}
	worker := task.record.Worker
	if taskmeta.ValidateSpawnGeneration(generation) != nil || task.State() != InFlight ||
		task.record.Completion != nil || task.record.LaunchIntent != nil ||
		worker == nil || worker.Generation != generation || worker.PendingAdoption ||
		worker.PendingGeneration != "" || worker.PendingLaunch != "" ||
		worker.Workspace == "" || worker.Tab == "" || worker.Machine != "" ||
		worker.Lease == (WorktreeLease{}) || worker.Lease.Remote != (RemoteLease{}) ||
		worker.Lease.State != LeaseHeld || worker.RemoteResult != (RemoteResult{}) || worker.Access != nil {
		return ErrInvalidTransition
	}
	return nil
}

// DiscardWorker records cancellation after the host verifies absence and inventory.
func (task Task) DiscardWorker(generation string) (Task, error) {
	if err := task.CheckDiscardWorker(generation); err != nil {
		return Task{}, err
	}
	if _, found := task.LaunchAbandonment(generation); found {
		return task, nil
	}
	snapshot := task.Snapshot()
	lease := snapshot.Worker.Lease
	snapshot.AbandonedLaunches = append(snapshot.AbandonedLaunches, LaunchAbandonment{
		Kind: DiscardedWorker, WorkerRetired: true, Generation: generation,
		Lease: lease, LeaseHolder: lease.Holder, Project: lease.Project,
		BaseCommit: lease.BaseCommit, Worktree: lease.Path, Branch: lease.Branch,
	})
	snapshot.Worker = nil
	snapshot.State = Queued
	return newTransition(snapshot)
}
