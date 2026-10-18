package taskstate

import "context"

func (store *Store) SelectRemoteResult(
	ctx context.Context,
	taskID, generation, commit, holder string,
) (Task, error) {
	return store.reserveWorkerResources(ctx, taskID, func(task Task) (Task, error) {
		return task.SelectRemoteResult(generation, commit, holder)
	})
}

func (store *Store) RecordRemoteResultLease(
	ctx context.Context,
	taskID, generation string,
	lease WorktreeLease,
) (Task, error) {
	return store.reserveWorkerResources(ctx, taskID, func(task Task) (Task, error) {
		return task.RecordRemoteResultLease(generation, lease)
	})
}

type primaryWorktreeClaim struct {
	project, path, branch, holder string
}

func primaryLeaseClaim(lease WorktreeLease) []primaryWorktreeClaim {
	if lease.State != LeaseHeld || lease.Remote != (RemoteLease{}) {
		return nil
	}
	return []primaryWorktreeClaim{
		{project: lease.Project, path: lease.Path, branch: lease.Branch, holder: lease.Holder},
	}
}

func primaryResultClaim(result RemoteResult, source WorktreeLease) []primaryWorktreeClaim {
	if result == (RemoteResult{}) || result.Lease.State == LeaseReturned {
		return nil
	}
	return []primaryWorktreeClaim{{project: source.Remote.PrimaryProject, path: result.Lease.Path,
		branch: source.Branch, holder: result.Holder}}
}

func historicalPrimaryClaims(snapshot Snapshot) []primaryWorktreeClaim {
	var claims []primaryWorktreeClaim
	receipts := snapshot.PriorReceipts
	if snapshot.Receipt != nil {
		receipts = append(receipts, *snapshot.Receipt)
	}
	for _, receipt := range receipts {
		claims = append(claims, primaryLeaseClaim(receipt.Lease)...)
		claims = append(claims, primaryResultClaim(receipt.RemoteResult, receipt.Lease)...)
	}
	for _, receipt := range snapshot.AbandonedLaunches {
		claims = append(claims, primaryLeaseClaim(receipt.Lease)...)
	}
	return claims
}

func activePrimaryClaims(snapshot Snapshot) []primaryWorktreeClaim {
	var claims []primaryWorktreeClaim
	if worker := snapshot.Worker; worker != nil {
		claims = append(claims, primaryResultClaim(worker.RemoteResult, worker.Lease)...)
		if worker.Machine == "" {
			claims = append(
				claims,
				primaryWorktreeClaim{project: worker.Project, path: worker.Worktree,
					branch: worker.Lease.Branch, holder: worker.Lease.Holder},
			)
		}
	}
	if launch := snapshot.LaunchIntent; launch != nil && launch.Machine == "" {
		claims = append(claims, primaryWorktreeClaim{project: launch.Project, path: launch.Worktree,
			branch: launch.Branch, holder: launch.LeaseHolder})
	}
	return claims
}

func primaryClaimsConflict(left, right primaryWorktreeClaim) bool {
	if left.path != "" && left.path == right.path {
		return true
	}
	if left.project != right.project {
		return false
	}
	return (left.branch != "" && left.branch == right.branch) ||
		(left.holder != "" && left.holder == right.holder)
}

func checkPrimaryResultReservations(tasks []Task, candidate Task) error {
	snapshot := candidate.Snapshot()
	if worker := snapshot.Worker; worker != nil && worker.RemoteResult != (RemoteResult{}) {
		claim := primaryResultClaim(worker.RemoteResult, worker.Lease)[0]
		for _, task := range tasks {
			claims := historicalPrimaryClaims(task.Snapshot())
			if task.ID() != candidate.ID() {
				claims = append(claims, activePrimaryClaims(task.Snapshot())...)
			}
			for _, existing := range claims {
				if primaryClaimsConflict(claim, existing) {
					return ErrWorkerConflict
				}
			}
		}
		return nil
	}
	claims := activePrimaryClaims(snapshot)
	for _, task := range tasks {
		other := task.Snapshot()
		var reserved []primaryWorktreeClaim
		if other.Worker != nil {
			reserved = append(
				reserved,
				primaryResultClaim(other.Worker.RemoteResult, other.Worker.Lease)...)
		}
		receipts := other.PriorReceipts
		if other.Receipt != nil {
			receipts = append(receipts, *other.Receipt)
		}
		for _, receipt := range receipts {
			reserved = append(reserved, primaryResultClaim(receipt.RemoteResult, receipt.Lease)...)
		}
		for _, claim := range claims {
			for _, existing := range reserved {
				if primaryClaimsConflict(claim, existing) {
					return ErrWorkerConflict
				}
			}
		}
	}
	return nil
}
