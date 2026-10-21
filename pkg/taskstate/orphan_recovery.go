package taskstate

import "strings"

// CheckRecoverOrphaned restricts recovery to a pending local pooled launch.
// The caller must exclude restart execution and confirm every endpoint is absent.
func (task Task) CheckRecoverOrphaned(generation string) error {
	if closure := task.record.Closure; closure != nil {
		if closure.Worker != nil && closure.Generation == generation {
			return nil
		}
		return ErrInvalidTransition
	}
	worker := task.record.Worker
	if task.State() != InFlight || task.record.Completion != nil ||
		task.record.LaunchIntent != nil || worker == nil || worker.Generation != generation ||
		!recoverablePendingWorker(*worker) {
		return ErrInvalidTransition
	}
	return nil
}

func recoverablePendingWorker(worker Worker) bool {
	if worker.PendingAdoption || (worker.PendingLaunch == "" && worker.PendingGeneration == "") {
		return false
	}
	if worker.Machine != "" || worker.Workspace == "" || worker.Tab == "" {
		return false
	}
	if worker.Access != nil || worker.RemoteResult != (RemoteResult{}) {
		return false
	}
	if worker.Lease == (WorktreeLease{}) || worker.Lease.Remote != (RemoteLease{}) {
		return false
	}
	return worker.Lease.State == LeaseHeld
}

// RecoverOrphaned closes a confirmed absent pending worker without releasing work.
func (task Task) RecoverOrphaned(generation, reason, closedOn string) (Task, error) {
	if err := task.CheckRecoverOrphaned(generation); err != nil {
		return Task{}, err
	}
	if !singleLine(reason) || strings.TrimSpace(reason) == "" || closedOn == "" ||
		!validDate(closedOn) {
		return Task{}, ErrInvalidTransition
	}
	if closure := task.record.Closure; closure != nil {
		if closure.Reason == reason && task.record.Closed == closedOn {
			return task, nil
		}
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	lease := worker.Lease
	snapshot.AbandonedLaunches = append(snapshot.AbandonedLaunches, LaunchAbandonment{
		Kind: OrphanedWorker, WorkerRetired: true, Generation: generation,
		Lease: lease, LeaseHolder: lease.Holder, Project: lease.Project,
		BaseCommit: lease.BaseCommit, Worktree: lease.Path, Branch: lease.Branch,
	})
	snapshot.Closure = &Closure{Generation: generation, Reason: reason, Worker: worker}
	snapshot.Worker = nil
	snapshot.State = Done
	snapshot.Definition.Closed = closedOn
	return newTransition(snapshot)
}

func validOrphanedClosure(selected record) bool {
	closure := selected.Closure
	worker := closure.Worker
	if worker == nil || worker.Generation != closure.Generation || !validWorker(*worker) ||
		!recoverablePendingWorker(*worker) {
		return false
	}
	receipt, found := (Task{record: selected}).LaunchAbandonment(closure.Generation)
	lease := receipt.Lease
	// The archive retains the original lease state after an explicit later return.
	lease.State = LeaseHeld
	return found && receipt.Kind == OrphanedWorker && lease == worker.Lease
}
