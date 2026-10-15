package taskops

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/tincan/pkg/inbox"
)

const scoutContract = "\n\n## Scout contract\n\nYou are a read-only scout. Never edit project files, commit, push, or open a PR. Use read-only commands and put scratch artifacts outside the project. Investigate only the assigned scope. Deliver a report with findings, evidence, limitations, and any open decisions as the body of concierge task reply send. Concierge archives each reply in its state root before acknowledging the assignment."

func (service *Service) archiveReply(message inbox.Message) error {
	reports, err := inbox.New(filepath.Join(service.root, "reports"))
	if err != nil {
		return err
	}
	reply, err := taskprotocol.DecodeReply(message.Body, message.Key, service.root)
	if err != nil {
		return err
	}
	recipient, err := inbox.ParseRecipient(reply.Task)
	if err != nil {
		return err
	}
	_, err = reports.Send(recipient, message.Key, message.Body)
	return err
}

// ReadScoutReport reads historical evidence independently of the worker mailbox.
// Deprecated: use ReadReport for any task kind.
func (service *Service) ReadScoutReport(
	ctx context.Context,
	taskID, replyID string,
) (taskprotocol.Reply, error) {
	return service.ReadReport(ctx, taskID, replyID)
}

// ReadReport reads historical evidence independently of the worker mailbox.
func (service *Service) ReadReport(
	ctx context.Context,
	taskID, replyID string,
) (taskprotocol.Reply, error) {
	id, err := inbox.ParseMessageID(replyID)
	if err != nil {
		return taskprotocol.Reply{}, err
	}
	if _, err := service.store.Read(ctx, taskID); err != nil {
		return taskprotocol.Reply{}, err
	}
	recipient, err := inbox.ParseRecipient(taskID)
	if err != nil {
		return taskprotocol.Reply{}, err
	}
	reports, err := inbox.New(filepath.Join(service.root, "reports"))
	if err != nil {
		return taskprotocol.Reply{}, err
	}
	message, err := reports.Read(recipient, id)
	if err != nil {
		return taskprotocol.Reply{}, err
	}
	reply, err := taskprotocol.DecodeReply(message.Body, message.Key, service.root)
	if err != nil {
		return taskprotocol.Reply{}, err
	}
	if reply.Task != taskID {
		return taskprotocol.Reply{}, errors.New("report belongs to another task")
	}
	return reply, nil
}

// ListReports returns saved reports in creation order, independently of task status.
func (service *Service) ListReports(ctx context.Context, taskID string) ([]inbox.Message, error) {
	if _, err := service.store.Read(ctx, taskID); err != nil {
		return nil, err
	}
	recipient, err := inbox.ParseRecipient(taskID)
	if err != nil {
		return nil, err
	}
	reports, err := inbox.New(filepath.Join(service.root, "reports"))
	if err != nil {
		return nil, err
	}
	return reports.List(recipient)
}
