package taskops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

// RetireStaleReply moves an obsolete reply to handled without accepting a result.
// The task lock prevents worker changes between the binding check and acknowledgement.
func (service *Service) RetireStaleReply(
	ctx context.Context,
	messageRoot, messageID string,
) (string, error) {
	id, err := inbox.ParseMessageID(messageID)
	if err != nil {
		return "", err
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return "", err
	}
	message, err := messages.Read(service.primaryRecipient, id)
	if errors.Is(err, os.ErrNotExist) {
		message, err = messages.ReadHandled(service.primaryRecipient, id)
	}
	if err != nil {
		return "", err
	}
	reply, err := taskprotocol.DecodeReply(message.Body, message.Key, service.root)
	if err != nil {
		return "", err
	}
	_, err = service.store.WithTask(ctx, reply.Task, func(locked *taskstate.LockedTask) error {
		if _, current := currentReplyWorker(locked.Current(), reply, messageID); current {
			return errors.New(
				"reply is current; inspect it with task reply check before accepting a result",
			)
		}
		if err := locked.CheckCurrent(); err != nil {
			return err
		}
		return messages.Acknowledge(service.primaryRecipient, id)
	})
	if err != nil {
		return "", fmt.Errorf("stale reply retirement requires a valid existing task: %w", err)
	}
	return reply.Task, nil
}
