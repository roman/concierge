package taskops

import (
	"context"
	"errors"
	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskprotocol"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
	"github.com/roman/tincan/pkg/inbox"
)

// CloseRetired records a closure decision without returning any retained lease.
func (service *Service) CloseRetired(
	ctx context.Context,
	messageRoot, taskID, generation, reason, closedOn string,
	client herdr.Client,
) (taskstate.Task, error) {
	updated, err := service.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if _, err := current.CloseRetired(generation, reason, closedOn); err != nil {
			return err
		}
		if worker := current.Snapshot().Worker; worker != nil {
			target, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
			if err != nil {
				return err
			}
			if err := client.ConfirmPaneGone(ctx, target); err != nil {
				return err
			}
			if err := service.checkClosureReplies(messageRoot, taskID, *worker); err != nil {
				return err
			}
		}
		if err := locked.CheckCurrent(); err != nil {
			return err
		}
		_, err := locked.Apply(func(task taskstate.Task) (taskstate.Task, error) {
			return task.CloseRetired(generation, reason, closedOn)
		})
		return err
	})
	if err != nil && !errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		return taskstate.Task{}, err
	}
	return updated, errors.Join(err, taskview.Rebuild(ctx, service.root))
}

func (service *Service) checkClosureReplies(
	messageRoot, taskID string,
	worker taskstate.Worker,
) error {
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return err
	}
	pending, err := messages.List(service.primaryRecipient)
	if err != nil {
		return err
	}
	for _, message := range pending {
		reply, err := taskprotocol.DecodeReply(message.Body, message.Key, service.root)
		if err == nil && reply.Task == taskID &&
			(reply.SpawnGeneration == worker.Generation || preservedReply(worker, message.ID.String())) {
			return errors.New(
				"close refused: pending bound reply requires review through task reply check",
			)
		}
	}
	return nil
}
