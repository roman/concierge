package taskops

import (
	"os"
	"testing"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/tincan/pkg/inbox"
)

func TestReplyAttachmentsSurviveMailboxCleanup(t *testing.T) {
	for _, binding := range []string{"bound", "unbound"} {
		t.Run(binding, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			bytes := []byte{0, 255, 128, '\n'}
			attachment, err := taskprotocol.NewAttachment("trace.bin", bytes)
			if err != nil {
				t.Fatal(err)
			}
			input := replyInput(assignment)
			if binding == "unbound" {
				input.AssignmentID = ""
			}
			input.Attachments = []taskprotocol.Attachment{attachment}
			result, err := service.SendReply(t.Context(), messages.Root(), input)
			if err != nil || result.Archive != ReplyConfirmed {
				t.Fatalf("send: %#v %v", result, err)
			}
			if err := os.RemoveAll(messages.Root()); err != nil {
				t.Fatal(err)
			}
			report, err := service.ReadReport(t.Context(), input.TaskID, result.Message.ID.String())
			if err != nil || len(report.Attachments) != 1 ||
				string(report.Attachments[0].Data) != string(bytes) {
				t.Fatalf("saved attachments: %+v %v", report, err)
			}
		})
	}
}

func TestInvalidAttachmentDoesNotPublishOrAcknowledge(t *testing.T) {
	for _, mutation := range []string{"checksum", "size", "duplicate", "oversized"} {
		t.Run(mutation, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			attachment, _ := taskprotocol.NewAttachment("evidence.txt", []byte("abc"))
			input := replyInput(assignment)
			input.Attachments = []taskprotocol.Attachment{attachment}
			switch mutation {
			case "checksum":
				input.Attachments[0].Data[0] = 'x'
			case "size":
				input.Attachments[0].Size++
			case "duplicate":
				input.Attachments = append(input.Attachments, attachment)
			case "oversized":
				input.Attachments[0].Data = make([]byte, taskprotocol.MaxAttachmentBytes+1)
			}
			if result, err := service.SendReply(t.Context(), messages.Root(), input); err == nil ||
				result.Message.ID.String() != "" {
				t.Fatalf("invalid reply published: %#v %v", result, err)
			}
			primary, _ := inbox.ParseRecipient(DefaultPrimaryRecipient)
			pending, err := messages.List(primary)
			if err != nil || len(pending) != 0 {
				t.Fatalf("partial publication: %v %v", pending, err)
			}
			if _, err := messages.Read(assignment.Recipient, assignment.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAttachmentRetryCannotReplacePublishedBytes(t *testing.T) {
	service, messages, assignment := assignmentFixture(t)
	attachment, _ := taskprotocol.NewAttachment("evidence.txt", []byte("abc"))
	input := replyInput(assignment)
	other, _ := taskprotocol.NewAttachment("other.txt", []byte("other"))
	input.Attachments = []taskprotocol.Attachment{attachment, other}
	first, err := service.SendReply(t.Context(), messages.Root(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Attachments = []taskprotocol.Attachment{other, attachment}
	retry, err := service.SendReply(t.Context(), messages.Root(), input)
	if err != nil || retry.Message.ID != first.Message.ID {
		t.Fatalf("reordered retry: %#v %v", retry, err)
	}
	input.Attachments = []taskprotocol.Attachment{attachment, other}
	input.Attachments[0], _ = taskprotocol.NewAttachment("evidence.txt", []byte("changed"))
	if _, err := service.SendReply(t.Context(), messages.Root(), input); err == nil {
		t.Fatal("changed retry accepted")
	}
	report, err := service.ReadReport(t.Context(), input.TaskID, first.Message.ID.String())
	if err != nil || string(report.Attachments[0].Data) != "abc" {
		t.Fatalf("original changed: %v", err)
	}
}
