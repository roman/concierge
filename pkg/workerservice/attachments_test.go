package workerservice

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestAttachmentSocketRoundTripAtTotalLimit(t *testing.T) {
	server, _, messages, request := dispatchFixture(t)
	socket, _, _ := serveSocketFixture(t, server, request.Task)
	request.Operation, request.Key, request.Body = workeraccess.SendReply, "result", "Done"
	data := bytes.Repeat([]byte{0, 128, 255, '\n'}, taskprotocol.MaxAttachmentBytes/4)
	for _, name := range []string{"first.bin", "second.bin"} {
		attachment, err := taskprotocol.NewAttachment(name, data)
		if err != nil {
			t.Fatal(err)
		}
		request.Attachments = append(request.Attachments, attachment)
	}
	response, err := Call(t.Context(), socket, request)
	if err != nil || response.Reply == nil || response.Outcome != Completed {
		t.Fatalf("socket reply: %+v %v", response, err)
	}
	if err := os.RemoveAll(messages.Root()); err != nil {
		t.Fatal(err)
	}
	report, err := server.service.ReadReport(t.Context(), request.Task, response.Reply.MessageID)
	if err != nil || len(report.Attachments) != 2 {
		t.Fatalf("saved reply: %v", err)
	}
	for _, attachment := range report.Attachments {
		if !bytes.Equal(attachment.Data, data) {
			t.Fatal("binary attachment changed during transport")
		}
	}
}

func TestWorkerAttachmentsCannotAccompanyOtherOperations(t *testing.T) {
	attachment, _ := taskprotocol.NewAttachment("evidence.txt", []byte("abc"))
	for _, operation := range workeraccess.Operations() {
		if operation == workeraccess.SendReply {
			continue
		}
		request := Request{
			Task:       "task-1",
			Generation: "gen-1",
			Secret:     "secret",
			Operation:  operation,
			MessageID: "k-" + strings.Repeat(
				"a",
				64,
			),
		}
		if operation == workeraccess.ListPending || operation == workeraccess.ReadSettings {
			request.MessageID = ""
		}
		if err := request.validate(); err != nil {
			t.Fatalf("invalid control request for %s: %v", operation, err)
		}
		request.Attachments = []taskprotocol.Attachment{attachment}
		if err := request.validate(); err == nil {
			t.Fatalf("attachments accepted for %s", operation)
		}
	}
}
