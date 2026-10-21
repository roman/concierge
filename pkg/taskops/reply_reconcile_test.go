package taskops

import (
	"context"
	"reflect"
	"testing"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestTrustedReconciliationPublishesTheOriginalRetiredGeneration(t *testing.T) {
	service, messages, assignment, _ := workerSessionFixture(t)
	ctx := context.Background()
	payload := replyoutbox.Payload{Root: service.root, Task: "task-1", Generation: "gen-existing",
		AssignmentID: assignment.ID.String(), Key: "original-result", Recipient: service.primaryRecipient.String(),
		Body: "Original reply retained across revocation\n"}
	attachment, err := taskprotocol.NewAttachment("evidence.bin", []byte{0, 128, 255})
	if err != nil {
		t.Fatal(err)
	}
	payload.Attachments = []taskprotocol.Attachment{attachment}
	if _, err := service.Finish(ctx, payload.Task, payload.Generation, taskstate.Close, "2026-10-17"); err != nil {
		t.Fatal(err)
	}
	before, err := service.store.Read(ctx, payload.Task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendReply(ctx, messages.Root(), ReplyInput{TaskID: payload.Task,
		Generation: payload.Generation, AssignmentID: payload.AssignmentID, Key: payload.Key, Body: payload.Body}); err == nil {
		t.Fatal("ordinary recovery created a reply for a retired generation")
	}
	first, err := service.ReconcileReply(ctx, messages.Root(), payload)
	if err != nil || first.Publication != ReplyConfirmed || first.Archive != ReplyConfirmed ||
		first.Assignment != ReplyAcknowledged {
		t.Fatalf("trusted reconciliation failed: %+v %v", first, err)
	}
	retried, err := service.ReconcileReply(ctx, messages.Root(), payload)
	if err != nil || retried.Message.ID != first.Message.ID {
		t.Fatalf("original reply identity changed: %+v %v", retried, err)
	}
	reply, err := taskprotocol.DecodeReply(first.Message.Body, first.Message.Key, payload.Root)
	if err != nil || reply.SpawnGeneration != payload.Generation || reply.Body != payload.Body ||
		reply.ReplyKey != payload.Key {
		t.Fatalf("reconciliation changed the original tuple: %+v %v", reply, err)
	}
	after, err := service.store.Read(ctx, payload.Task)
	if !reflect.DeepEqual(reply.Attachments, payload.Attachments) {
		t.Fatal("reconciliation lost original attachments")
	}
	if err != nil || !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
		t.Fatalf("reconciliation reopened or changed the task: %v", err)
	}
}

func TestTrustedReconciliationRejectsAnotherRootOrRecipient(t *testing.T) {
	service, messages, assignment, _ := workerSessionFixture(t)
	payload := replyoutbox.Payload{Root: service.root, Task: "task-1", Generation: "gen-existing",
		AssignmentID: assignment.ID.String(), Key: "original-result", Recipient: service.primaryRecipient.String(), Body: "Original"}
	for _, field := range []string{"root", "recipient"} {
		changed := payload
		if field == "root" {
			changed.Root = "/other/state"
		} else {
			changed.Recipient = "other-primary"
		}
		if _, err := service.ReconcileReply(context.Background(), messages.Root(), changed); err == nil {
			t.Fatalf("reconciliation accepted another %s", field)
		}
	}
	pending, err := messages.List(service.primaryRecipient)
	if err != nil || len(pending) != 0 {
		t.Fatalf("scope mismatch published a reply: count=%d error=%v", len(pending), err)
	}
}
