package taskops

import (
	"context"
	"errors"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
	"github.com/roman/concierge/pkg/worktree"
)

// RecoverOrphaned preserves a pending local worker after confirmed resource absence.
// The host must hold its task and endpoint reservations throughout this operation.
func (service *Service) RecoverOrphaned(
	ctx context.Context, taskID, generation, reason, closedOn string, client herdr.Client,
) (taskstate.Task, error) {
	updated, err := service.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if _, err := current.RecoverOrphaned(generation, reason, closedOn); err != nil {
			return err
		}
		if current.Snapshot().Closure != nil {
			return nil
		}
		worker := current.Snapshot().Worker
		target, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
		if err != nil {
			return err
		}
		if err := client.ConfirmPaneGone(ctx, target); err != nil {
			return err
		}
		scoped, err := client.ForEndpoint(worker.Machine, worker.Session)
		if err != nil {
			return err
		}
		if err := scoped.CheckLaunchAbsent(ctx, herdr.LaunchResources{
			Name: worker.Name, Label: worker.OwnedWorkspaceLabel, Tab: worker.Tab, Pane: worker.Pane,
		}); err != nil {
			return err
		}
		if err := checkWorktreeLease(ctx, worker.Lease, service.pool); err != nil {
			return err
		}
		// An absent endpoint can still leave a detached process in the worktree.
		if err := service.pool.ConfirmReservation(ctx, worker.Lease.Project, worktree.Lease{
			ID: worker.Lease.ID, Holder: worker.Lease.Holder, Path: worker.Lease.Path,
		}); err != nil {
			return err
		}
		if err := locked.CheckCurrent(); err != nil {
			return err
		}
		_, err = locked.Apply(func(task taskstate.Task) (taskstate.Task, error) {
			return task.RecoverOrphaned(generation, reason, closedOn)
		})
		return err
	})
	if err != nil && !errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		return taskstate.Task{}, err
	}
	return updated, errors.Join(err, taskview.Rebuild(ctx, service.root))
}
