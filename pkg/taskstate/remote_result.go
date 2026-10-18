package taskstate

import (
	"encoding/hex"
	"strings"
)

type RemoteResultPhase string

const (
	RemoteResultSelected       RemoteResultPhase = "selected"
	RemoteResultLeaseRequested RemoteResultPhase = "lease_requested"
	RemoteResultLeaseCaptured  RemoteResultPhase = "lease_captured"
	RemoteResultReady          RemoteResultPhase = "ready"
)

// RemoteResult retains primary delivery resources separately from remote
// supervision paths. The selected commit cannot change during materialization.
type RemoteResult struct {
	Commit string            `json:"commit"`
	Holder string            `json:"holder"`
	Phase  RemoteResultPhase `json:"phase"`
	Lease  WorktreeLease     `json:"lease,omitzero"`
}

func validRemoteResult(result RemoteResult, source WorktreeLease) bool {
	if result == (RemoteResult{}) {
		return true
	}
	if source.Remote == (RemoteLease{}) || !validWorktreeLease(source) ||
		!singleLine(result.Holder) {
		return false
	}
	decoded, err := hex.DecodeString(result.Commit)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) ||
		strings.ToLower(result.Commit) != result.Commit {
		return false
	}
	switch result.Phase {
	case RemoteResultSelected, RemoteResultLeaseRequested:
		return result.Lease == (WorktreeLease{})
	case RemoteResultLeaseCaptured, RemoteResultReady:
		lease := result.Lease
		return lease != (WorktreeLease{}) && validWorktreeLease(lease) &&
			lease.Remote == (RemoteLease{}) && lease.Holder == result.Holder &&
			lease.Project == source.Remote.PrimaryProject && lease.Branch == source.Branch &&
			lease.BaseCommit == result.Commit
	default:
		return false
	}
}

func (task Task) remoteResultWorker(generation string) (*Worker, error) {
	worker := task.record.Worker
	if task.State() != InFlight || task.record.Completion != nil ||
		task.record.LaunchIntent != nil ||
		worker == nil {
		return nil, ErrInvalidTransition
	}
	if worker.Generation != generation || worker.Lease.Remote == (RemoteLease{}) ||
		worker.PendingAdoption || worker.PendingLaunch != "" || worker.PendingGeneration != "" ||
		task.Definition().Kind == "scout" {
		return nil, ErrInvalidTransition
	}
	return worker, nil
}

func (task Task) SelectRemoteResult(generation, commit, holder string) (Task, error) {
	worker, err := task.remoteResultWorker(generation)
	if err != nil {
		return Task{}, err
	}
	if previous := worker.RemoteResult; previous != (RemoteResult{}) {
		if previous.Commit == commit && previous.Holder == holder {
			return task, nil
		}
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	snapshot.Worker.RemoteResult = RemoteResult{
		Commit: commit,
		Holder: holder,
		Phase:  RemoteResultSelected,
	}
	return newTransition(snapshot)
}

// RequestRemoteResultLease precedes the non-idempotent primary pool acquisition.
func (task Task) RequestRemoteResultLease(generation string) (Task, error) {
	worker, err := task.remoteResultWorker(generation)
	if err != nil || worker.RemoteResult == (RemoteResult{}) {
		return Task{}, ErrInvalidTransition
	}
	if worker.RemoteResult.Phase != RemoteResultSelected {
		return task, nil
	}
	snapshot := task.Snapshot()
	snapshot.Worker.RemoteResult.Phase = RemoteResultLeaseRequested
	return newTransition(snapshot)
}

func (task Task) RecordRemoteResultLease(generation string, lease WorktreeLease) (Task, error) {
	worker, err := task.remoteResultWorker(generation)
	if err != nil || worker.RemoteResult == (RemoteResult{}) || lease.State != LeaseHeld {
		return Task{}, ErrInvalidTransition
	}
	if worker.RemoteResult.Lease != (WorktreeLease{}) {
		if worker.RemoteResult.Lease == lease {
			return task, nil
		}
		return Task{}, ErrInvalidTransition
	}
	if worker.RemoteResult.Phase != RemoteResultLeaseRequested {
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	snapshot.Worker.RemoteResult.Lease = lease
	snapshot.Worker.RemoteResult.Phase = RemoteResultLeaseCaptured
	return newTransition(snapshot)
}

func (task Task) ConfirmRemoteResult(generation string) (Task, error) {
	worker, err := task.remoteResultWorker(generation)
	if err != nil {
		return Task{}, err
	}
	if worker.RemoteResult.Phase == RemoteResultReady {
		return task, nil
	}
	if worker.RemoteResult.Phase != RemoteResultLeaseCaptured {
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	snapshot.Worker.RemoteResult.Phase = RemoteResultReady
	return newTransition(snapshot)
}
