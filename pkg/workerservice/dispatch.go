package workerservice

import (
	"context"
	"errors"
	"os"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

type Config struct {
	Root         string
	MessageRoot  string
	Recipient    inbox.Recipient
	ReadSettings func(string) (taskops.WorkerSettings, error)
}

type Server struct {
	config     Config
	service    *taskops.Service
	admission  admissionLimiter
	operations chan struct{}
}

func New(config Config) (*Server, error) {
	for _, path := range []string{config.Root, config.MessageRoot} {
		if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
			return nil, err
		}
	}
	if _, err := inbox.ParseRecipient(config.Recipient.String()); err != nil {
		return nil, err
	}
	service, err := taskops.NewWithPrimaryRecipient(config.Root, config.Recipient)
	if err != nil {
		return nil, err
	}
	return &Server{
		config:     config,
		service:    service,
		operations: make(chan struct{}, workerConnections),
	}, nil
}

func (server *Server) perform(ctx context.Context, request Request) Response {
	if err := request.validate(); err != nil {
		return Response{Outcome: Invalid, Error: ErrInvalidRequest.Error()}
	}
	session, err := server.service.WorkerSession(server.config.MessageRoot,
		request.Task, request.Generation, []byte(request.Secret))
	if err != nil {
		return workerFailure(Response{}, err)
	}
	response := Response{Outcome: Completed}
	switch request.Operation {
	case workeraccess.ListPending:
		response.Pending, err = session.Pending(ctx)
	case workeraccess.ReadBootstrap:
		var bootstrap taskops.BootstrapInfo
		bootstrap, err = session.ReadBootstrap(ctx, request.MessageID)
		if err == nil {
			response.Bootstrap = &bootstrap
		}
	case workeraccess.AckBootstrap:
		err = session.AcknowledgeBootstrap(ctx, request.MessageID)
		if err == nil {
			response.Bootstrap = &taskops.BootstrapInfo{
				Kind: "task-bootstrap", Task: request.Task, Generation: request.Generation,
			}
		}
	case workeraccess.ReadSettings:
		var settings taskops.WorkerSettings
		settings, err = session.Settings(ctx, server.config.ReadSettings)
		if err == nil {
			response.Settings = &settings
		}
	case workeraccess.Receive, workeraccess.AckAssignment:
		var status taskops.AssignmentStatus
		if request.Operation == workeraccess.Receive {
			status, err = session.ReadAssignment(ctx, request.MessageID)
		} else {
			status, err = session.AcknowledgeAssignment(ctx, request.MessageID)
		}
		if err == nil {
			response.Assignment = &Assignment{Status: status.State, Body: status.Body}
		}
	case workeraccess.SendReply:
		var delivery taskops.ReplyDelivery
		delivery, err = session.SendReply(
			ctx,
			request.Key,
			request.MessageID,
			request.Body,
			request.Attachments...)
		if delivery.Message.ID.String() != "" {
			response.Reply = &Reply{
				Root: server.config.Root, Recipient: server.config.Recipient.String(),
				MessageID:   delivery.Message.ID.String(),
				Assignment:  delivery.Assignment,
				Publication: delivery.Publication,
				Archive:     delivery.Archive,
			}
		}
	}
	if err != nil {
		return workerFailure(response, err)
	}
	return response
}

func workerFailure(response Response, err error) Response {
	if errors.Is(err, workeraccess.ErrDenied) || errors.Is(err, taskstate.ErrTaskNotFound) ||
		errors.Is(err, os.ErrNotExist) {
		return Response{Outcome: Denied, Error: workeraccess.ErrDenied.Error()}
	}
	response.Outcome, response.Error = Failed, err.Error()
	if response.Reply != nil || errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		response.Outcome = Uncertain
	}
	return response
}
