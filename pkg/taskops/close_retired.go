package taskops

import (
	"context"
	"errors"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
)

// CloseRetired records a closure decision without returning any retained lease.
func (service *Service) CloseRetired(
	ctx context.Context, taskID, generation, reason, closedOn string,
) (taskstate.Task, error) {
	updated, err := service.store.Update(
		ctx,
		taskID,
		func(task taskstate.Task) (taskstate.Task, error) {
			return task.CloseRetired(generation, reason, closedOn)
		},
	)
	if err != nil && !errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		return taskstate.Task{}, err
	}
	return updated, errors.Join(err, taskview.Rebuild(ctx, service.root))
}
