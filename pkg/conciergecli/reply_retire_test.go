package conciergecli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestTaskReplyRetireStaleUsesSelectedMailbox(t *testing.T) {
	root := lifecycleRoot(t)
	startFixtureTask(t, root)
	messageRoot := t.TempDir()
	assignment := assignmentMessage(t, root, messageRoot, "gen-1", "Work")
	recipient, _ := inbox.ParseRecipient("operator")
	service, err := taskops.NewWithPrimaryRecipient(root, recipient)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	delivery, err := service.SendReply(ctx, messageRoot, taskops.ReplyInput{
		TaskID: "task-1", Generation: "gen-1", Key: "answer", AssignmentID: assignment.ID.String(), Body: "Complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--root",
		root,
		"--message-root",
		messageRoot,
		"--recipient",
		"operator",
		"task",
		"reply",
		"retire-stale",
		delivery.Message.ID.String(),
	}
	var output bytes.Buffer
	if code := Run(ctx, args, strings.NewReader(""), &output, &output); code != 1 ||
		!strings.Contains(output.String(), "reply is current") {
		t.Fatalf("current reply: code=%d, %s", code, &output)
	}
	if _, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Close, "2026-10-12"); err != nil {
		t.Fatal(err)
	}
	before, err := heartbeat.Collect(ctx, root, messageRoot, "operator", "/missing-herdr")
	if err != nil || !strings.Contains(before.Text, "pending_replies: 1\n") {
		t.Fatalf("heartbeat before retirement: %v, %s", err, before.Text)
	}
	for range 2 {
		output.Reset()
		if code := Run(ctx, args, strings.NewReader(""), &output, &output); code != 0 ||
			output.String() != "status: retired\ntask: \"task-1\"\n" {
			t.Fatalf("retire stale: code=%d, %s", code, &output)
		}
	}
	after, err := heartbeat.Collect(ctx, root, messageRoot, "operator", "/missing-herdr")
	if err != nil || !strings.Contains(after.Text, "pending_replies: 0\n") ||
		strings.Contains(after.Text, "pending_reply:") || len(after.Replies) != 0 {
		t.Fatalf("heartbeat after retirement: %v, %s", err, after.Text)
	}
	t.Logf(
		"heartbeat bytes before=%d after=%d saved=%d",
		len(before.Text),
		len(after.Text),
		len(before.Text)-len(after.Text),
	)
}
