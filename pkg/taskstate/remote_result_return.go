package taskstate

// RetiredRemoteResult retains delivery evidence after the native worker leaves.
func (task Task) RetiredRemoteResult(generation string) (RemoteResult, bool) {
	if receipt := task.record.Receipt; receipt != nil && receipt.Generation == generation {
		return receipt.RemoteResult, receipt.RemoteResult != (RemoteResult{})
	}
	for _, receipt := range task.record.PriorReceipts {
		if receipt.Generation == generation {
			return receipt.RemoteResult, receipt.RemoteResult != (RemoteResult{})
		}
	}
	return RemoteResult{}, false
}

// RecordPrimaryResultLeaseReturned does not change the native lease receipt.
func (task Task) RecordPrimaryResultLeaseReturned(
	generation string,
	expected WorktreeLease,
) (Task, error) {
	result, found := task.RetiredRemoteResult(generation)
	if !found || expected == (WorktreeLease{}) || expected.Remote != (RemoteLease{}) ||
		!validWorktreeLease(expected) {
		return Task{}, ErrInvalidTransition
	}
	returned := expected
	returned.State = LeaseReturned
	if result.Lease == returned {
		return task, nil
	}
	if result.Lease != expected || expected.State != LeaseHeld {
		return Task{}, ErrInvalidTransition
	}
	snapshot := task.Snapshot()
	if snapshot.Receipt != nil && snapshot.Receipt.Generation == generation {
		snapshot.Receipt.RemoteResult.Lease = returned
	}
	for index := range snapshot.PriorReceipts {
		if snapshot.PriorReceipts[index].Generation == generation {
			snapshot.PriorReceipts[index].RemoteResult.Lease = returned
		}
	}
	return newTransition(snapshot)
}
