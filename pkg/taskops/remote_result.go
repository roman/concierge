package taskops

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

var ErrRemoteResultUnavailable = errors.New(
	"remote result materialization requires the primary's native export transport",
)

var ErrRemoteResultBusy = errors.New(
	"remote result materialization is already running; retry after it completes",
)

type RemoteResultExporter interface {
	Export(context.Context, taskstate.Task, string, io.Writer) (string, error)
}

func (service *Service) WithRemoteResultExporter(exporter RemoteResultExporter) *Service {
	configured := *service
	configured.remoteResults = exporter
	return &configured
}

// MaterializeRemoteResult retains one primary lease for the selected commit.
// Store reservations run outside WithTask to preserve the global lock order.
func (service *Service) MaterializeRemoteResult(
	ctx context.Context,
	taskID, generation string,
) (taskstate.Task, error) {
	if service.remoteResults == nil || service.remoteResultMu == nil {
		return taskstate.Task{}, ErrRemoteResultUnavailable
	}
	if !filepath.IsAbs(service.git.Executable) || !filepath.IsAbs(service.pool.Executable) {
		return taskstate.Task{}, ErrRemoteResultUnavailable
	}
	if !service.remoteResultMu.TryLock() {
		return taskstate.Task{}, ErrRemoteResultBusy
	}
	defer service.remoteResultMu.Unlock()
	task, err := service.store.Read(ctx, taskID)
	if err != nil {
		return taskstate.Task{}, err
	}
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil ||
		snapshot.LaunchIntent != nil ||
		worker == nil {
		return taskstate.Task{}, taskstate.ErrInvalidTransition
	}
	if worker.Generation != generation || worker.Lease.Remote == (taskstate.RemoteLease{}) ||
		worker.Lease.Remote.PrimaryRoot != service.root || snapshot.Definition.Kind == "scout" ||
		worker.PendingAdoption || worker.PendingGeneration != "" || worker.PendingLaunch != "" {
		return taskstate.Task{}, taskstate.ErrInvalidTransition
	}
	result := worker.RemoteResult
	reference := remoteResultReference(result.Holder)
	if result == (taskstate.RemoteResult{}) ||
		service.git.VerifyResult(
			ctx,
			worker.Lease.Remote.PrimaryProject,
			result.Commit,
			reference,
		) != nil {
		file, err := os.CreateTemp("", "concierge-primary-result-*")
		if err != nil {
			return taskstate.Task{}, err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		commit, err := service.remoteResults.Export(ctx, task, result.Commit, file)
		if err != nil {
			return taskstate.Task{}, err
		}
		if result == (taskstate.RemoteResult{}) {
			holder := fmt.Sprintf(
				"concierge:result:%x",
				sha256.Sum256([]byte(service.root+"\x00"+taskID+"\x00"+generation)),
			)
			task, err = service.store.SelectRemoteResult(ctx, taskID, generation, commit, holder)
			if err != nil {
				return taskstate.Task{}, err
			}
			result = task.Snapshot().Worker.RemoteResult
			reference = remoteResultReference(result.Holder)
		}
		if commit != result.Commit {
			return taskstate.Task{}, worktree.ErrIdentity
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return taskstate.Task{}, err
		}
		if err := service.git.ImportResult(ctx, worker.Lease.Remote.PrimaryProject, result.Commit, reference, file); err != nil {
			return taskstate.Task{}, err
		}
	}
	task, err = service.captureRemoteResultLease(ctx, task, generation)
	if err != nil {
		return taskstate.Task{}, err
	}
	result = task.Snapshot().Worker.RemoteResult
	if err := checkWorktreeLease(ctx, result.Lease, service.pool); err != nil {
		return taskstate.Task{}, err
	}
	specification := worktree.Specification{Project: result.Lease.Project, Path: result.Lease.Path,
		Branch: result.Lease.Branch, BaseCommit: result.Commit}
	if err := service.git.PrepareLeased(ctx, specification); err != nil {
		return taskstate.Task{}, err
	}
	if err := service.git.VerifyReturn(ctx, specification); err != nil {
		return taskstate.Task{}, err
	}
	return service.store.Update(ctx, taskID, func(current taskstate.Task) (taskstate.Task, error) {
		return current.ConfirmRemoteResult(generation)
	})
}

func remoteResultReference(holder string) string {
	return fmt.Sprintf("refs/concierge/results/%x", sha256.Sum256([]byte(holder)))
}

func (service *Service) captureRemoteResultLease(
	ctx context.Context,
	task taskstate.Task,
	generation string,
) (taskstate.Task, error) {
	worker := task.Snapshot().Worker
	result := worker.RemoteResult
	if result.Lease != (taskstate.WorktreeLease{}) {
		return task, nil
	}
	project := worker.Lease.Remote.PrimaryProject
	var acquired worktree.Lease
	var err error
	if result.Phase == taskstate.RemoteResultSelected {
		if _, err := service.store.Update(ctx, task.ID(), func(current taskstate.Task) (taskstate.Task, error) {
			return current.RequestRemoteResultLease(generation)
		}); err != nil {
			return taskstate.Task{}, err
		}
		acquired, err = service.pool.Acquire(ctx, project, result.Holder)
	} else if result.Phase == taskstate.RemoteResultLeaseRequested {
		var found bool
		acquired, found, err = service.pool.Find(ctx, project, result.Holder)
		if err == nil && !found {
			err = fmt.Errorf("primary result acquisition remains unresolved for holder %q; inspect the pool before retrying", result.Holder)
		}
	} else {
		return taskstate.Task{}, taskstate.ErrInvalidTransition
	}
	if err != nil {
		return taskstate.Task{}, err
	}
	lease := taskstate.WorktreeLease{ID: acquired.ID, Holder: acquired.Holder, Project: project,
		Path: acquired.Path, Branch: worker.Lease.Branch, BaseCommit: result.Commit, State: taskstate.LeaseHeld}
	return service.store.RecordRemoteResultLease(ctx, task.ID(), generation, lease)
}
