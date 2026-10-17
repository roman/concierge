package taskops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

type PendingMessage struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

type BootstrapInfo struct {
	Kind       string `json:"kind"`
	Task       string `json:"task"`
	Generation string `json:"generation"`
}

type WorkerSettings struct {
	Repo     string `json:"repo"`
	Merge    string `json:"merge"`
	Delivery string `json:"delivery"`
}

func (session *WorkerSession) withTask(
	ctx context.Context,
	operation workeraccess.Operation,
	perform func(*taskstate.LockedTask) error,
) error {
	if err := session.preflight(ctx, operation); err != nil {
		return err
	}
	_, err := session.service.store.WithTask(
		ctx,
		session.taskID,
		func(task *taskstate.LockedTask) error {
			if err := session.authorize(operation)(task.Current()); err != nil {
				return err
			}
			if err := task.CheckCurrent(); err != nil {
				return err
			}
			return perform(task)
		},
	)
	return err
}

func (session *WorkerSession) Pending(ctx context.Context) ([]PendingMessage, error) {
	var pending []PendingMessage
	err := session.withTask(ctx, workeraccess.ListPending, func(*taskstate.LockedTask) error {
		recipient, _ := inbox.ParseRecipient(session.taskID)
		messages, err := session.messages.List(recipient)
		if err != nil {
			return err
		}
		for _, message := range messages {
			pending = append(pending, PendingMessage{
				ID: message.ID.String(), Key: message.Key, CreatedAt: message.CreatedAt,
			})
		}
		return nil
	})
	return pending, err
}

func (session *WorkerSession) ReadBootstrap(
	ctx context.Context,
	messageID string,
) (BootstrapInfo, error) {
	return session.bootstrap(ctx, messageID, workeraccess.ReadBootstrap)
}

func (session *WorkerSession) AcknowledgeBootstrap(ctx context.Context, messageID string) error {
	_, err := session.bootstrap(ctx, messageID, workeraccess.AckBootstrap)
	return err
}

func (session *WorkerSession) bootstrap(
	ctx context.Context,
	messageID string,
	operation workeraccess.Operation,
) (BootstrapInfo, error) {
	id, err := inbox.ParseMessageID(messageID)
	if err != nil {
		return BootstrapInfo{}, err
	}
	var result BootstrapInfo
	err = session.withTask(ctx, operation, func(*taskstate.LockedTask) error {
		recipient, _ := inbox.ParseRecipient(session.taskID)
		message, err := session.messages.Read(recipient, id)
		if operation == workeraccess.AckBootstrap && errors.Is(err, os.ErrNotExist) {
			message, err = session.messages.ReadHandled(recipient, id)
		}
		if err != nil {
			return err
		}
		var bootstrap taskBootstrap
		decoder := json.NewDecoder(bytes.NewBufferString(message.Body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&bootstrap) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
			bootstrap.Kind != "task-bootstrap" || bootstrap.Task != session.taskID ||
			bootstrap.SpawnGeneration != session.generation ||
			bootstrap.State != filepath.Join(session.service.root, "tasks") {
			return errors.New("message is not a current task bootstrap")
		}
		result = BootstrapInfo{
			Kind:       bootstrap.Kind,
			Task:       session.taskID,
			Generation: session.generation,
		}
		if operation == workeraccess.AckBootstrap {
			return session.messages.Acknowledge(recipient, id)
		}
		return nil
	})
	return result, err
}

func (session *WorkerSession) Settings(
	ctx context.Context,
	read func(string) (WorkerSettings, error),
) (WorkerSettings, error) {
	var settings WorkerSettings
	err := session.withTask(ctx, workeraccess.ReadSettings, func(task *taskstate.LockedTask) error {
		if read == nil {
			return errors.New("host has no project settings reader")
		}
		repo := task.Current().Definition().Repo
		var err error
		settings, err = read(repo)
		if err != nil {
			return err
		}
		settings.Repo = repo
		if (settings.Merge != "on" && settings.Merge != "off") ||
			(settings.Delivery != "pr" && settings.Delivery != "fast-forward") {
			return errors.New("invalid task project settings")
		}
		return nil
	})
	return settings, err
}
