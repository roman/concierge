package hostservice

import (
	"context"
	"errors"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

const ReconcileReply Operation = "reconcile_reply"

func (server *Server) reconcileReply(
	ctx context.Context,
	payload replyoutbox.Payload,
) (Response, error) {
	delivery, err := server.service.ReconcileReply(ctx, server.config.MessageRoot, payload)
	response := Response{}
	if delivery.Message.ID.String() != "" {
		response.Reconciled = &workerservice.Reply{
			Root:        server.config.Root,
			Recipient:   server.config.Recipient.String(),
			MessageID:   delivery.Message.ID.String(),
			Publication: delivery.Publication,
			Archive:     delivery.Archive,
			Assignment:  delivery.Assignment,
		}
		if err != nil {
			err = errors.Join(ErrOutcomeUnknown, err)
		}
	}
	return response, err
}

func validateReconciliationResponse(response Response, request Request) error {
	if request.Operation != ReconcileReply || validateRequest(request) != nil {
		return errors.New("host returned an unrelated reconciliation result")
	}
	if response.Task != nil || response.Transition != nil || response.Resolution != nil ||
		response.Assignment != nil || response.Stopped != nil || response.Control != nil ||
		response.Status != nil || response.Supervision != nil || response.Checkpoint != nil ||
		response.Abandoned != nil || len(response.RemovedStoreLinks) != 0 {
		return errors.New("host returned other operation data during reconciliation")
	}
	payload := request.Reconciliation
	if reply := response.Reconciled; reply != nil &&
		(reply.Root != payload.Root || reply.Recipient != payload.Recipient) {
		return errors.New("host changed the original reply binding")
	}
	var outcome workerservice.Outcome
	switch response.Outcome {
	case Completed:
		outcome = workerservice.Completed
	case Failed:
		outcome = workerservice.Failed
	case Uncertain:
		outcome = workerservice.Uncertain
	case Invalid:
		outcome = workerservice.Invalid
	case Unauthorized:
		outcome = workerservice.Denied
	case Busy:
		outcome = workerservice.Busy
	default:
		return errors.New("host returned an unknown reconciliation outcome")
	}
	return (workerservice.Response{Outcome: outcome, Error: response.Error, Reply: response.Reconciled}).ValidateFor(
		workerservice.Request{Task: payload.Task, Generation: payload.Generation,
			Operation: workeraccess.SendReply, MessageID: payload.AssignmentID},
	)
}
