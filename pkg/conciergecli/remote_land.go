package conciergecli

import (
	"errors"

	"github.com/roman/concierge/pkg/taskstate"
)

type landingSource struct {
	project, worktree string
	lease             taskstate.WorktreeLease
	commit            string
}

func workerLandingSource(worker taskstate.Worker) (landingSource, error) {
	if worker.Machine == "" {
		return landingSource{
			project:  worker.Project,
			worktree: worker.Worktree,
			lease:    worker.Lease,
		}, nil
	}
	result := worker.RemoteResult
	if result.Phase != taskstate.RemoteResultReady || result.Lease.State != taskstate.LeaseHeld ||
		result.Lease.Remote != (taskstate.RemoteLease{}) || result.Lease.Project != worker.Lease.Remote.PrimaryProject ||
		result.Lease.Branch != worker.Lease.Branch || result.Lease.BaseCommit != result.Commit {
		return landingSource{}, errors.New(
			"remote landing requires a materialized result and its held primary lease",
		)
	}
	return landingSource{
		project:  result.Lease.Project,
		worktree: result.Lease.Path,
		lease:    result.Lease,
		commit:   result.Commit,
	}, nil
}
