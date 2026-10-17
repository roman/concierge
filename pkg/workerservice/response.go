package workerservice

import (
	"errors"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

var errInvalidResponse = errors.New("invalid worker response")

func validateResponse(response Response, request Request) error {
	payloads := 0
	for _, present := range []bool{len(response.Pending) > 0, response.Bootstrap != nil,
		response.Settings != nil, response.Assignment != nil, response.Reply != nil} {
		if present {
			payloads++
		}
	}
	if response.Reply != nil {
		if _, err := taskmeta.ParseAbsolutePath(response.Reply.Root); err != nil {
			return errInvalidResponse
		}
		if recipient, err := inbox.ParseRecipient(response.Reply.Recipient); err != nil ||
			recipient.String() != response.Reply.Recipient {
			return errInvalidResponse
		}
		if response.Reply.Publication != taskops.ReplyConfirmed ||
			(response.Reply.Archive != taskops.ReplyConfirmed && response.Reply.Archive != taskops.ReplyUnconfirmed) {
			return errInvalidResponse
		}
		if (request.MessageID == "") != (response.Reply.Assignment == taskops.ReplyUnbound) {
			return errInvalidResponse
		}
		if _, err := inbox.ParseMessageID(response.Reply.MessageID); err != nil {
			return errInvalidResponse
		}
		switch response.Reply.Assignment {
		case taskops.ReplyUnbound,
			taskops.ReplyAcknowledgementUnconfirmed,
			taskops.ReplyAcknowledged:
		default:
			return errInvalidResponse
		}
	}
	switch response.Outcome {
	case Denied, Invalid, Busy:
		if response.Error == "" || payloads != 0 {
			return errInvalidResponse
		}
		return nil
	case Failed, Uncertain:
		if response.Error == "" || payloads > 1 ||
			(payloads == 1 && (request.Operation != workeraccess.SendReply || response.Reply == nil)) {
			return errInvalidResponse
		}
		return nil
	case Completed:
		if response.Error != "" {
			return errInvalidResponse
		}
	default:
		return errInvalidResponse
	}
	if request.Operation == workeraccess.ListPending {
		if payloads > 1 || (payloads == 1 && len(response.Pending) == 0) {
			return errInvalidResponse
		}
		for _, message := range response.Pending {
			if _, err := inbox.ParseMessageID(message.ID); err != nil ||
				message.CreatedAt.IsZero() {
				return errInvalidResponse
			}
		}
		return nil
	}
	if payloads != 1 {
		return errInvalidResponse
	}
	switch request.Operation {
	case workeraccess.ReadBootstrap, workeraccess.AckBootstrap:
		if bootstrap := response.Bootstrap; bootstrap != nil &&
			bootstrap.Kind == "task-bootstrap" &&
			bootstrap.Task == request.Task &&
			bootstrap.Generation == request.Generation {
			return nil
		}
	case workeraccess.ReadSettings:
		if settings := response.Settings; settings != nil && settings.Repo != "" &&
			(settings.Merge == "on" || settings.Merge == "off") &&
			(settings.Delivery == "pr" || settings.Delivery == "fast-forward") {
			return nil
		}
	case workeraccess.Receive, workeraccess.AckAssignment:
		if assignment := response.Assignment; assignment != nil {
			if request.Operation == workeraccess.Receive &&
				assignment.Status == taskops.AssignmentCurrent &&
				strings.TrimSpace(assignment.Body) != "" {
				return nil
			}
			if assignment.Body == "" && (assignment.Status == taskops.AssignmentStale ||
				(request.Operation == workeraccess.AckAssignment && assignment.Status == taskops.AssignmentAcknowledged)) {
				return nil
			}
		}
	case workeraccess.SendReply:
		if response.Reply != nil &&
			response.Reply.Archive == taskops.ReplyConfirmed &&
			response.Reply.Assignment != taskops.ReplyAcknowledgementUnconfirmed {
			return nil
		}
	}
	return errInvalidResponse
}
