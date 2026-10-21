package taskops

import (
	"context"
	"errors"

	"github.com/roman/concierge/pkg/replyoutbox"
)

var ErrReplyScope = errors.New("original reply root and recipient must match this primary host")

// ReconcileReply is reserved for the authenticated primary host. It republishes
// the original tuple without granting a revoked worker access or changing its
// generation. Ordinary worker replies retain their existing recovery rules.
func (service *Service) ReconcileReply(
	ctx context.Context,
	messageRoot string,
	payload replyoutbox.Payload,
) (ReplyDelivery, error) {
	if err := payload.Validate(); err != nil {
		return ReplyDelivery{}, err
	}
	if payload.Root != service.root || payload.Recipient != service.primaryRecipient.String() {
		return ReplyDelivery{}, ErrReplyScope
	}
	if _, err := service.store.Read(ctx, payload.Task); err != nil {
		return ReplyDelivery{}, err
	}
	return service.sendReply(ctx, messageRoot, ReplyInput{
		TaskID: payload.Task, Generation: payload.Generation, Key: payload.Key,
		AssignmentID: payload.AssignmentID, Body: payload.Body, Attachments: payload.Attachments,
	}, nil, trustedOriginalReply)
}
