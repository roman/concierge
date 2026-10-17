package taskops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

func workerSessionFixture(
	t *testing.T,
) (*Service, *inbox.Store, inbox.Message, workeraccess.Secret) {
	t.Helper()
	service, input, _, _ := remoteObservedFixture(t)
	grant, secret, err := workeraccess.Issue(
		input.TaskID,
		input.Generation,
		workeraccess.Operations(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.Update(context.Background(), input.TaskID, func(current taskstate.Task) (taskstate.Task, error) {
		return current.InstallWorkerAccess(grant)
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := inbox.New(service.root + "-messages")
	if err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient(input.TaskID)
	body, _ := json.Marshal(taskprotocol.Assignment{
		Kind: "task-assignment", State: filepath.Join(service.root, "tasks"),
		Task: input.TaskID, SpawnGeneration: input.Generation, Body: "Complete the assigned work",
	})
	message, err := messages.Send(recipient, "assignment", string(body))
	if err != nil {
		t.Fatal(err)
	}
	return service, messages, message, secret
}

func TestWorkerSessionAuthorizesInsideAssignmentLock(t *testing.T) {
	service, messages, message, secret := workerSessionFixture(t)
	ctx := context.Background()
	session, err := service.WorkerSession(messages.Root(), "task-1", "gen-existing", secret.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	status, err := session.ReadAssignment(ctx, message.ID.String())
	if err != nil || status.State != AssignmentCurrent ||
		status.Body != "Complete the assigned work" {
		t.Fatalf("current read: %+v %v", status, err)
	}
	if _, err := service.store.Update(ctx, "task-1", func(current taskstate.Task) (taskstate.Task, error) {
		return current.RevokeWorkerAccess("gen-existing")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AcknowledgeAssignment(ctx, message.ID.String()); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) {
		t.Fatalf("session ignored revocation after construction: %v", err)
	}
	if _, err := messages.Read(message.Recipient, message.ID); err != nil {
		t.Fatalf("denied acknowledgement changed the assignment: %v", err)
	}
}

func TestWorkerSessionDenialHasNoReplyOrTaskMutation(t *testing.T) {
	for _, trial := range []string{"wrong secret", "wrong task", "wrong generation", "revoked"} {
		t.Run(trial, func(t *testing.T) {
			service, messages, message, secret := workerSessionFixture(t)
			ctx := context.Background()
			taskID, generation, token := "task-1", "gen-existing", secret.Bytes()
			switch trial {
			case "wrong secret":
				token = []byte("incorrect")
			case "wrong task":
				taskID = "task-2"
				if _, err := service.Create(ctx, taskstate.Definition{ID: taskID, Title: "Other", Repo: "exmachina", Kind: "task"}); err != nil {
					t.Fatal(err)
				}
			case "wrong generation":
				generation = "gen-other"
			case "revoked":
				if _, err := service.store.Update(ctx, "task-1", func(current taskstate.Task) (taskstate.Task, error) {
					return current.RevokeWorkerAccess("gen-existing")
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := service.store.Read(ctx, "task-1")
			beforeBytes, _ := before.Encode()
			session, err := service.WorkerSession(messages.Root(), taskID, generation, token)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.SendReply(ctx, "result", message.ID.String(), "Done"); !errors.Is(
				err,
				workeraccess.ErrDenied,
			) {
				t.Fatalf("denied send: %v", err)
			}
			after, _ := service.store.Read(ctx, "task-1")
			afterBytes, _ := after.Encode()
			if !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("denied send changed task state")
			}
			pending, err := messages.List(service.primaryRecipient)
			if err != nil || len(pending) != 0 {
				t.Fatalf("denied send published a reply: %v", err)
			}
			if _, err := messages.Read(message.Recipient, message.ID); err != nil {
				t.Fatalf("denied send acknowledged the assignment: %v", err)
			}
		})
	}
}

func TestWorkerSessionPublishesCurrentReply(t *testing.T) {
	service, messages, message, secret := workerSessionFixture(t)
	session, err := service.WorkerSession(messages.Root(), "task-1", "gen-existing", secret.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.SendReply(
		context.Background(),
		"result",
		message.ID.String(),
		"Completed",
	)
	if err != nil || result.Assignment != ReplyAcknowledged || result.Message.ID.String() == "" {
		t.Fatalf("current reply: %+v %v", result, err)
	}
	if _, err := messages.ReadHandled(message.Recipient, message.ID); err != nil {
		t.Fatalf("reply did not acknowledge its assignment: %v", err)
	}
}
