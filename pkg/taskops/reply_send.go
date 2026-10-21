package taskops

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

// ReplyInput identifies one worker result and its optional assignment.
type ReplyInput struct {
	TaskID       string
	Generation   string
	Key          string
	AssignmentID string
	Body         string
	Attachments  []taskprotocol.Attachment
}

// ReplyAcknowledgement describes assignment handling after reply publication.
type ReplyAcknowledgement string

const (
	ReplyUnbound                    ReplyAcknowledgement = "not_requested"
	ReplyAcknowledgementUnconfirmed ReplyAcknowledgement = "unconfirmed"
	ReplyAcknowledged               ReplyAcknowledgement = "acknowledged"
)

type ReplyConfirmation string

type replyPublicationMode int

const (
	currentGenerationReply replyPublicationMode = iota
	trustedOriginalReply
)

const (
	ReplyConfirmed    ReplyConfirmation = "confirmed"
	ReplyUnconfirmed  ReplyConfirmation = "unconfirmed"
	ReplyNotRequested ReplyConfirmation = "not_requested"
)

// ReplyDelivery preserves confirmed delivery when assignment handling fails.
type ReplyDelivery struct {
	Message     inbox.Message
	Assignment  ReplyAcknowledgement
	Publication ReplyConfirmation
	Archive     ReplyConfirmation
}

// SendReply publishes current work or confirms an exact existing reply on retry.
func (service *Service) SendReply(
	ctx context.Context, messageRoot string, input ReplyInput,
) (ReplyDelivery, error) {
	return service.sendReply(ctx, messageRoot, input, nil, currentGenerationReply)
}

func (service *Service) sendReply(
	ctx context.Context,
	messageRoot string,
	input ReplyInput,
	authorize func(taskstate.Task) error,
	mode replyPublicationMode,
) (ReplyDelivery, error) {
	if err := taskprotocol.ValidateAttachments(input.Attachments); err != nil {
		return ReplyDelivery{}, err
	}
	if err := taskmeta.ValidateSpawnGeneration(input.Generation); err != nil {
		return ReplyDelivery{}, err
	}
	if strings.TrimSpace(input.Key) == "" {
		return ReplyDelivery{}, inbox.ErrMissingKey
	}
	if strings.TrimSpace(input.Body) == "" {
		return ReplyDelivery{}, inbox.ErrBlankBody
	}
	worker, err := inbox.ParseRecipient(input.TaskID)
	if err != nil {
		return ReplyDelivery{}, err
	}
	var assignmentID inbox.MessageID
	if input.AssignmentID != "" {
		assignmentID, err = inbox.ParseMessageID(input.AssignmentID)
		if err != nil {
			return ReplyDelivery{}, err
		}
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return ReplyDelivery{}, err
	}
	var result ReplyDelivery
	_, err = service.store.WithTask(ctx, input.TaskID, func(task *taskstate.LockedTask) error {
		if authorize != nil {
			if err := authorize(task.Current()); err != nil {
				return err
			}
		}
		if input.AssignmentID != "" {
			if err := taskprotocol.ValidateBoundAssignment(messages, service.root, input.TaskID, input.Generation, assignmentID); err != nil {
				return err
			}
		}
		replyKey, suffix := "", input.Key
		if input.AssignmentID != "" {
			replyKey, suffix = input.Key, input.AssignmentID
		}
		envelope, err := json.Marshal(taskprotocol.Reply{
			Kind: "task-reply", State: filepath.Join(service.root, "tasks"),
			Task: input.TaskID, SpawnGeneration: input.Generation,
			AssignmentID: input.AssignmentID, ReplyKey: replyKey, Body: input.Body,
			Attachments: taskprotocol.CanonicalAttachments(input.Attachments),
		})
		if err != nil {
			return err
		}
		if len(input.Attachments) != 0 && len(envelope) > taskprotocol.MaxReplyBytes {
			return taskprotocol.ErrInvalidReply
		}
		key := fmt.Sprintf(
			"task-reply:%s:%s:%s:%s",
			service.root,
			input.TaskID,
			input.Generation,
			suffix,
		)
		publish := messages.ConfirmExisting
		bound, bindingErr := assignmentWorker(task.Current())
		if mode == trustedOriginalReply ||
			bindingErr == nil && bound.Generation == input.Generation {
			publish = messages.Send
		}
		message, err := publish(service.primaryRecipient, key, string(envelope))
		if err != nil {
			return err
		}
		result = ReplyDelivery{Message: message, Assignment: ReplyUnbound,
			Publication: ReplyConfirmed, Archive: ReplyNotRequested}
		if input.AssignmentID != "" {
			result.Assignment = ReplyAcknowledgementUnconfirmed
		}
		if len(input.Attachments) != 0 || mode == trustedOriginalReply || authorize != nil ||
			input.AssignmentID != "" ||
			task.Current().Definition().Kind == "scout" {
			result.Archive = ReplyUnconfirmed
			if err := service.archiveReply(message); err != nil {
				return err
			}
			result.Archive = ReplyConfirmed
		}
		if input.AssignmentID == "" {
			return nil
		}
		result.Assignment = ReplyAcknowledgementUnconfirmed
		if err := clearHandledPromptProgress(task, input.AssignmentID); err != nil {
			return err
		}
		if err := messages.Acknowledge(worker, assignmentID); err != nil {
			return err
		}
		result.Assignment = ReplyAcknowledged
		return nil
	})
	return result, err
}
