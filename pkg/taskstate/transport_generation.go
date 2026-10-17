package taskstate

// ConfirmTransportGeneration records confirmed secret delivery and tunnel setup.
// Generation advancement clears the confirmation. Recovery can then resume
// interrupted setup.
func (task Task) ConfirmTransportGeneration(generation string) (Task, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if snapshot.State != InFlight || snapshot.Completion != nil || snapshot.Definition.Hold != nil {
		return Task{}, ErrInvalidTransition
	}
	if worker == nil || worker.Machine == "" || worker.Generation != generation {
		return Task{}, ErrInvalidTransition
	}
	if worker.PendingAdoption || worker.PendingGeneration != "" || worker.PendingLaunch != "" {
		return Task{}, ErrInvalidTransition
	}
	access := worker.Access
	if access == nil || access.Revoked || access.Task != task.ID() ||
		access.Generation != generation || access.Validate() != nil {
		return Task{}, ErrInvalidTransition
	}
	worker.TransportGeneration = generation
	return newTransition(snapshot)
}
