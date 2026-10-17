package taskstate

import (
	"slices"

	"github.com/roman/concierge/pkg/workeraccess"
)

// InstallWorkerAccess binds one verifier before delivering its secret.
// A retry cannot silently issue another secret for the same generation.
func (task Task) InstallWorkerAccess(grant workeraccess.Grant) (Task, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if worker == nil || worker.Machine == "" || snapshot.State != InFlight ||
		snapshot.Completion != nil || worker.PendingAdoption || worker.PendingGeneration != "" ||
		worker.PendingLaunch != "" || grant.Task != task.ID() || grant.Generation != worker.Generation ||
		grant.Revoked || grant.Validate() != nil {
		return Task{}, ErrInvalidTransition
	}
	if current := worker.Access; current != nil {
		if current.Task == grant.Task && current.Generation == grant.Generation &&
			current.Verifier == grant.Verifier && current.Revoked == grant.Revoked &&
			slices.Equal(current.Operations, grant.Operations) {
			return task, nil
		}
		return Task{}, ErrInvalidTransition
	}
	worker.Access = &grant
	return newTransition(snapshot)
}

func (task Task) RevokeWorkerAccess(generation string) (Task, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if worker == nil || worker.Machine == "" || worker.Generation != generation {
		return Task{}, ErrInvalidTransition
	}
	if worker.Access == nil || worker.Access.Revoked {
		return task, nil
	}
	worker.Access.Revoked = true
	return newTransition(snapshot)
}
