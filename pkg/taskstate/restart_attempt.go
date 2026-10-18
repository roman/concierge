package taskstate

// RequestRestartAgent records a possible remote start before its submission.
// A missing native registration cannot justify a second start after a crash.
func (task Task) RequestRestartAgent(generation string) (Task, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if snapshot.State != InFlight || snapshot.Completion != nil || worker == nil {
		return Task{}, ErrInvalidTransition
	}
	if worker.Machine == "" || worker.Lease.Remote == (RemoteLease{}) ||
		worker.Generation != generation || worker.PendingLaunch != generation {
		return Task{}, ErrInvalidTransition
	}
	if len(worker.Restarts) == 0 || worker.Restarts[len(worker.Restarts)-1].To != generation {
		return Task{}, ErrInvalidTransition
	}
	worker.LaunchAttemptGeneration = generation
	return newTransition(snapshot)
}
