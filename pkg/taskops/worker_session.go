package taskops

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

// WorkerSession exposes only task-scoped operations to the worker socket.
// Each operation checks the current grant under the same lock as its mutation.
type WorkerSession struct {
	service    *Service
	messages   *inbox.Store
	taskID     string
	generation string
	secret     []byte
}

func (service *Service) WorkerSession(
	messageRoot, taskID, generation string,
	secret []byte,
) (*WorkerSession, error) {
	if _, err := taskmeta.ParseTaskID(taskID); err != nil {
		return nil, workeraccess.ErrDenied
	}
	if taskmeta.ValidateSpawnGeneration(generation) != nil {
		return nil, workeraccess.ErrDenied
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return nil, err
	}
	return &WorkerSession{service: service, messages: messages, taskID: taskID,
		generation: generation, secret: slices.Clone(secret)}, nil
}

func (session *WorkerSession) authorize(
	operation workeraccess.Operation,
) func(taskstate.Task) error {
	return func(current taskstate.Task) error {
		snapshot := current.Snapshot()
		grant := workeraccess.Grant{}
		if snapshot.Worker != nil && snapshot.Worker.Access != nil {
			grant = *snapshot.Worker.Access
		}
		if err := grant.Authorize(session.taskID, session.generation, operation, session.secret); err != nil {
			return err
		}
		bound, err := assignmentWorker(current)
		if err != nil || bound.Machine == "" || bound.Generation != session.generation ||
			current.ID() != session.taskID {
			return workeraccess.ErrDenied
		}
		return nil
	}
}

func (session *WorkerSession) preflight(
	ctx context.Context,
	operation workeraccess.Operation,
) error {
	current, err := session.service.store.Read(ctx, session.taskID)
	if errors.Is(err, taskstate.ErrTaskNotFound) || errors.Is(err, os.ErrNotExist) {
		empty := workeraccess.Grant{}
		return empty.Authorize(session.taskID, session.generation, operation, session.secret)
	}
	if err != nil {
		return err
	}
	return session.authorize(operation)(current)
}

func (session *WorkerSession) ReadAssignment(
	ctx context.Context,
	messageID string,
) (AssignmentStatus, error) {
	if err := session.preflight(ctx, workeraccess.Receive); err != nil {
		return AssignmentStatus{}, err
	}
	return session.service.receiveAssignment(ctx, session.messages.Root(), session.taskID,
		messageID, session.generation, readAssignment, session.authorize(workeraccess.Receive))
}

func (session *WorkerSession) AcknowledgeAssignment(
	ctx context.Context,
	messageID string,
) (AssignmentStatus, error) {
	if err := session.preflight(ctx, workeraccess.AckAssignment); err != nil {
		return AssignmentStatus{}, err
	}
	return session.service.receiveAssignment(
		ctx,
		session.messages.Root(),
		session.taskID,
		messageID,
		session.generation,
		acknowledgeAssignment,
		session.authorize(workeraccess.AckAssignment),
	)
}

func (session *WorkerSession) SendReply(
	ctx context.Context,
	key, assignmentID, body string,
) (ReplyDelivery, error) {
	if err := session.preflight(ctx, workeraccess.SendReply); err != nil {
		return ReplyDelivery{}, err
	}
	return session.service.sendReply(ctx, session.messages.Root(), ReplyInput{
		TaskID: session.taskID, Generation: session.generation, Key: key,
		AssignmentID: assignmentID, Body: body,
	}, session.authorize(workeraccess.SendReply))
}
