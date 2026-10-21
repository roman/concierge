package conciergecli

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

func remoteReplyContext(secretFile, task, generation string) (replyoutbox.Context, error) {
	binding, err := replyoutbox.ReadContext(filepath.Dir(secretFile))
	if err != nil || binding.Task != task || binding.Generation != generation {
		return replyoutbox.Context{}, replyoutbox.ErrContext
	}
	return binding, nil
}

func sendRemoteReply(
	ctx context.Context,
	socket, secretFile string,
	request workerservice.Request,
) (workerservice.Response, string, error) {
	binding, err := remoteReplyContext(secretFile, request.Task, request.Generation)
	if err != nil {
		return workerservice.Response{}, "", err
	}
	payload := replyoutbox.Payload{Root: binding.Root, Task: request.Task,
		Generation: request.Generation, AssignmentID: request.MessageID, Key: request.Key,
		Recipient: binding.Recipient, Body: request.Body, Attachments: request.Attachments}
	store, err := replyoutbox.New(filepath.Dir(secretFile))
	if err != nil {
		return workerservice.Response{}, "", err
	}
	path, err := store.Path(payload)
	if err != nil {
		return workerservice.Response{}, "", err
	}
	record, err := store.Stage(ctx, payload)
	if err != nil {
		if !errors.Is(err, replyoutbox.ErrRevoked) && !errors.Is(err, replyoutbox.ErrConflict) {
			return workerservice.Response{}, "", err
		}
		return workerservice.Response{}, path, err
	}
	if record.Complete() {
		return confirmedRemoteReply(record), path, nil
	}
	secret, err := workeraccess.ReadSecretFile(secretFile)
	if err != nil {
		return workerservice.Response{}, path, err
	}
	if err := binding.Match(request.Task, request.Generation, secret); err != nil {
		return workerservice.Response{}, path, err
	}
	request.Secret = string(secret)
	response, callError := workerservice.Call(ctx, socket, request)
	// Persist known receipts or revocation even if the caller cancels after the
	// response. This bounded local write does not issue another network request.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if errors.Is(callError, workeraccess.ErrDenied) {
		_, err := store.Revoke(persist, payload)
		return response, path, errors.Join(callError, err)
	}
	if reply := response.Reply; reply != nil {
		if reply.Root != payload.Root || reply.Recipient != payload.Recipient {
			return workerservice.Response{}, path, replyoutbox.ErrContext
		}
		_, err := store.Confirm(persist, payload, replyoutbox.Progress{
			MessageID: reply.MessageID, PublicationConfirmed: reply.Publication == taskops.ReplyConfirmed,
			ArchiveConfirmed:       reply.Archive == taskops.ReplyConfirmed,
			AssignmentAcknowledged: reply.Assignment == taskops.ReplyAcknowledged,
		})
		callError = errors.Join(callError, err)
	}
	return response, path, callError
}

func confirmedRemoteReply(record replyoutbox.Record) workerservice.Response {
	acknowledgement := taskops.ReplyUnbound
	if record.Payload.AssignmentID != "" {
		acknowledgement = taskops.ReplyAcknowledged
	}
	return workerservice.Response{Outcome: workerservice.Completed, Reply: &workerservice.Reply{
		Root: record.Payload.Root, Recipient: record.Payload.Recipient, MessageID: record.Progress.MessageID,
		Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed, Assignment: acknowledgement,
	}}
}
