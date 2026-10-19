package taskops

import (
	"context"
	"fmt"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/presentation"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
)

// Stop closes a generation's confirmed pane while preserving its task record.
// Cleanup remains separate so failed focus restoration can be retried.
func (service *Service) Stop(
	ctx context.Context,
	taskID, generation string,
	client herdr.Client,
) error {
	if err := taskmeta.ValidateSpawnGeneration(generation); err != nil {
		return err
	}
	_, err := service.store.WithTask(ctx, taskID, func(task *taskstate.LockedTask) error {
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		current := task.Current()
		if current.HasReceipt(generation, taskstate.Close) ||
			current.HasReceipt(generation, taskstate.Retain) {
			return nil
		}
		if err := CheckStop(current, generation); err != nil {
			return err
		}
		worker := current.Snapshot().Worker
		target, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
		if err != nil {
			return err
		}
		return presentation.CloseBoundPane(
			ctx,
			client,
			target,
			worker.Workspace,
			worker.Tab,
			task.CheckCurrent,
		)
	})
	return err
}

// CheckStop validates the confirmed binding before stopping its supervision.
func CheckStop(task taskstate.Task, generation string) error {
	if err := taskmeta.ValidateSpawnGeneration(generation); err != nil {
		return err
	}
	if task.HasReceipt(generation, taskstate.Close) ||
		task.HasReceipt(generation, taskstate.Retain) {
		return nil
	}
	worker := task.Snapshot().Worker
	if intent := task.Snapshot().LaunchIntent; worker == nil && intent != nil &&
		intent.Generation == generation {
		return fmt.Errorf(
			"%w: the initial launch has no confirmed worker; inspect and close its recorded pane, then use concierge task abandon %s --generation %s",
			taskstate.ErrInvalidTransition,
			task.ID(),
			generation,
		)
	}
	if worker == nil || worker.Generation != generation || worker.PendingAdoption ||
		worker.PendingGeneration != "" ||
		worker.PendingLaunch != "" ||
		worker.Workspace == "" ||
		worker.Tab == "" {
		return taskstate.ErrInvalidTransition
	}
	_, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
	return err
}
