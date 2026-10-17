package hostservice

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workerservice"
)

func TestUnauthorizedReconciliationWritesNoTaskOrReply(t *testing.T) {
	server, root, socket := fixture(t, nil)
	response, err := Call(context.Background(), socket, Request{Operation: ReconcileReply,
		Reconciliation: &replyoutbox.Payload{
			Root:       root,
			Task:       "task-1",
			Generation: "original-generation",
			Recipient:  "concierge",
			Key:        "result",
			Body:       "Retained result",
		}})
	if err != nil || response.Outcome != Unauthorized {
		t.Fatalf("reconciliation did not require the primary: %+v %v", response, err)
	}
	if _, err := os.Stat(server.config.MessageRoot); !os.IsNotExist(err) {
		t.Fatalf("unauthorized reconciliation opened the mailbox: %v", err)
	}
}

func TestReconciliationResponsePreservesOnlyValidReceipts(t *testing.T) {
	id := "k-" + strings.Repeat("a", 64)
	request := Request{Operation: ReconcileReply, Reconciliation: &replyoutbox.Payload{
		Root: "/primary/state", Task: "task-1", Generation: "old-generation", Recipient: "concierge",
		AssignmentID: id, Key: "original-result", Body: "Original result"}}
	reply := workerservice.Reply{
		Root:        request.Reconciliation.Root,
		Recipient:   request.Reconciliation.Recipient,
		MessageID:   id,
		Publication: taskops.ReplyConfirmed,
		Archive:     taskops.ReplyConfirmed,
		Assignment:  taskops.ReplyAcknowledged,
	}
	if err := validateResponse(Response{Outcome: Completed, Reconciled: &reply}, request); err != nil {
		t.Fatal(err)
	}
	reply.Archive, reply.Assignment = taskops.ReplyUnconfirmed, taskops.ReplyAcknowledgementUnconfirmed
	if err := validateResponse(Response{Outcome: Uncertain, Error: "archive failed", Reconciled: &reply}, request); err != nil {
		t.Fatalf("valid partial receipts were discarded: %v", err)
	}
	if err := validateResponse(Response{Outcome: Completed, Reconciled: &reply}, request); err == nil {
		t.Fatal("partial reconciliation was reported complete")
	}
	reply.Archive, reply.Assignment = taskops.ReplyConfirmed, taskops.ReplyAcknowledged
	reply.Root = "/other/state"
	if err := validateResponse(Response{Outcome: Completed, Reconciled: &reply}, request); err == nil {
		t.Fatal("reconciliation changed the original root")
	}
	request.Intake = intakeRequest().Intake
	if err := validateRequest(request); err == nil {
		t.Fatal("reconciliation accepted another operation's payload")
	}
}
