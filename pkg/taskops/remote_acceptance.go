package taskops

import (
	"context"

	"github.com/roman/concierge/pkg/taskstate"
)

func (service *Service) prepareRemoteAcceptance(
	ctx context.Context,
	taskID, generation string,
	mode taskstate.CompletionMode,
	closedOn string,
) error {
	task, err := service.store.Read(ctx, taskID)
	if err != nil {
		return err
	}
	if task.HasReceipt(generation, mode) {
		return nil
	}
	if err := task.CheckResultTransition(generation, mode, closedOn); err != nil {
		return err
	}
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if snapshot.Completion != nil || worker == nil || snapshot.Definition.Kind == "scout" ||
		worker.Lease.Remote == (taskstate.RemoteLease{}) {
		return nil
	}
	_, err = service.MaterializeRemoteResult(ctx, taskID, generation)
	return err
}
