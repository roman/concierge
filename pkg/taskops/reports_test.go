package taskops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/tincan/pkg/inbox"
)

func TestReportsKeepReworkRepliesSeparate(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	ctx := context.Background()
	var replies []ReplyDelivery
	for _, body := range []string{"Build the feature", "Rework the feature"} {
		assignment, err := service.SendAssignment(ctx, root, "task-1", body, body,
			herdr.Client{SessionRunner: runner})
		if err != nil {
			t.Fatal(err)
		}
		input := replyInput(assignment.Message)
		input.Body = body + " completed"
		reply, err := service.SendReply(ctx, root, input)
		if err != nil {
			t.Fatal(err)
		}
		replies = append(replies, reply)
		retry, err := service.SendReply(ctx, root, input)
		if err != nil || retry.Message.ID != reply.Message.ID {
			t.Fatalf("retry: %#v, %v", retry, err)
		}
	}
	archive, _ := inbox.New(filepath.Join(service.root, "reports"))
	recipient, _ := inbox.ParseRecipient("task-1")
	reports, err := archive.List(recipient)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports: %#v, %v", reports, err)
	}
	for index, reply := range replies {
		if reports[index].ID != reply.Message.ID || reports[index].Body != reply.Message.Body {
			t.Fatalf("report %d: %#v", index, reports[index])
		}
	}
}

func TestReportsExcludeCoordinationAndUnboundTaskReplies(t *testing.T) {
	service, messages, assignment := assignmentFixture(t)
	if _, err := messages.Send(service.primaryRecipient, "coordination", "A question for the primary"); err != nil {
		t.Fatal(err)
	}
	input := replyInput(assignment)
	input.AssignmentID = ""
	if _, err := service.SendReply(context.Background(), messages.Root(), input); err != nil {
		t.Fatal(err)
	}
	archive, _ := inbox.New(filepath.Join(service.root, "reports"))
	recipient, _ := inbox.ParseRecipient("task-1")
	reports, err := archive.List(recipient)
	if err != nil || len(reports) != 0 {
		t.Fatalf("unexpected reports: %#v, %v", reports, err)
	}
}

func TestScoutUnboundReplyStillArchived(t *testing.T) {
	service, messages, assignment := assignmentFixture(t)
	makeScout(t, service)
	input := replyInput(assignment)
	input.AssignmentID = ""
	reply, err := service.SendReply(context.Background(), messages.Root(), input)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.ReadScoutReport(
		context.Background(),
		"task-1",
		reply.Message.ID.String(),
	)
	if err != nil || report.Body != input.Body {
		t.Fatalf("scout report: %#v, %v", report, err)
	}
}
