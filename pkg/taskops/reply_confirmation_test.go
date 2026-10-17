package taskops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReplyDeliveryConfirmsPublicationArchiveAndAckSeparately(t *testing.T) {
	for _, stage := range []string{"complete", "archive failure", "ack failure", "unbound local"} {
		t.Run(stage, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			input := replyInput(assignment)
			wantArchive, wantAck := ReplyConfirmed, ReplyAcknowledged
			switch stage {
			case "archive failure":
				if err := os.WriteFile(filepath.Join(service.root, "reports"), []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
				wantArchive, wantAck = ReplyUnconfirmed, ReplyAcknowledgementUnconfirmed
			case "ack failure":
				handled := filepath.Join(messages.Root(), "inbox", "task-1", "handled")
				if err := os.Rename(handled, handled+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(handled, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
				wantAck = ReplyAcknowledgementUnconfirmed
			case "unbound local":
				input.AssignmentID = ""
				wantArchive, wantAck = ReplyNotRequested, ReplyUnbound
			}
			delivery, err := service.SendReply(context.Background(), messages.Root(), input)
			wantError := stage == "archive failure" || stage == "ack failure"
			if (err != nil) != wantError || delivery.Publication != ReplyConfirmed ||
				delivery.Archive != wantArchive || delivery.Assignment != wantAck {
				t.Fatalf("stage=%s delivery=%+v error=%v", stage, delivery, err)
			}
		})
	}
}

func TestRemoteReplyRequiresArchiveEvenWithoutAssignment(t *testing.T) {
	service, messages, _, secret := workerSessionFixture(t)
	session, err := service.WorkerSession(messages.Root(), "task-1", "gen-existing", secret.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := session.SendReply(context.Background(), "result", "", "Completed remotely")
	if err != nil || delivery.Publication != ReplyConfirmed || delivery.Archive != ReplyConfirmed ||
		delivery.Assignment != ReplyUnbound {
		t.Fatalf("remote reply lacks archive confirmation: %+v %v", delivery, err)
	}
	if _, err := service.ReadReport(context.Background(), "task-1", delivery.Message.ID.String()); err != nil {
		t.Fatalf("confirmed remote archive is unreadable: %v", err)
	}
}
