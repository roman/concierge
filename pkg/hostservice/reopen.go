package hostservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/roman/concierge/pkg/herdr"
)

func (server *Server) reopen(ctx context.Context, taskID string) (Response, error) {
	if server.config.Recovery == RecoveryMaintenance {
		return Response{}, errors.New("reopen requires normal supervised recovery")
	}
	task, err := server.store.Read(ctx, taskID)
	if err != nil {
		return Response{}, err
	}
	if err := task.CheckReopen(); err != nil {
		return Response{}, err
	}
	bound := task.Snapshot().Worker
	release, err := server.controllers.quiesceOperation(
		ctx,
		controllerKey{kind: taskController, id: taskID},
		controllerEndpoint{
			machine: bound.Machine,
			session: bound.Session,
			pane:    bound.Pane,
		},
		Reopen,
	)
	if err != nil {
		return Response{}, err
	}
	defer release()
	updated, err := server.service.Reopen(
		ctx,
		taskID,
		herdr.NewClient(server.config.HerdrExecutable),
	)
	if err != nil {
		return Response{}, err
	}
	// The replacement controller must acquire the endpoint after the operation releases it.
	release()
	if _, err := server.supervision(ctx, Supervise, SupervisionRequest{TaskID: taskID}); err != nil {
		return Response{}, fmt.Errorf(
			"task reopened; supervised recovery remains incomplete; retry task reopen %s: %w",
			taskID,
			err,
		)
	}
	updated, err = server.store.Read(ctx, updated.ID())
	if err != nil {
		return Response{}, err
	}
	snapshot := updated.Snapshot()
	return Response{Transition: &snapshot}, nil
}
