package taskops

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRetireStaleReplyProtectsCurrentAndInvalidReplies(t *testing.T) {
	for _, state := range []string{"current", "completed", "new generation", "missing task", "corrupt task", "wrong key", "wrong root"} {
		t.Run(state, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			ctx := context.Background()
			delivery, err := service.SendReply(ctx, messages.Root(), replyInput(assignment))
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(service.root, "records", "task-1.json")
			switch state {
			case "completed":
				_, err = service.Finish(ctx, "task-1", "gen-1", taskstate.Close, "2026-10-12")
			case "new generation":
				_, err = service.store.Update(
					ctx,
					"task-1",
					func(task taskstate.Task) (taskstate.Task, error) {
						snapshot := task.Snapshot()
						snapshot.Worker.Generation = "gen-2"
						return taskstate.NewMigrated(snapshot)
					},
				)
			case "missing task":
				err = os.Remove(record)
			case "corrupt task":
				err = os.WriteFile(record, []byte("invalid"), 0600)
			case "wrong key":
				delivery.Message.Key = "unrelated"
				writeMessageFixture(t, messages.Root(), "pending", delivery.Message)
			case "wrong root":
				delivery.Message.Body = `{"kind":"task-reply","state":"/another/tasks"}`
				writeMessageFixture(t, messages.Root(), "pending", delivery.Message)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(record)
			wantRetired := state == "completed" || state == "new generation"
			for range 2 {
				taskID, err := service.RetireStaleReply(
					ctx,
					messages.Root(),
					delivery.Message.ID.String(),
				)
				if (err == nil) != wantRetired || (wantRetired && taskID != "task-1") {
					t.Fatalf("retire: task=%q, error=%v", taskID, err)
				}
			}
			pending, err := messages.List(service.primaryRecipient)
			if err != nil || (len(pending) == 0) != wantRetired {
				t.Fatalf("pending: %v, %v", pending, err)
			}
			if wantRetired {
				handled, err := messages.ReadHandled(service.primaryRecipient, delivery.Message.ID)
				if err != nil || !reflect.DeepEqual(handled, delivery.Message) {
					t.Fatalf("handled reply changed: %v", err)
				}
				report, err := service.ReadReport(ctx, "task-1", delivery.Message.ID.String())
				if err != nil || report.Body != replyInput(assignment).Body {
					t.Fatalf("archived report lost: %v", err)
				}
			}
			after, _ := os.ReadFile(record)
			if string(before) != string(after) {
				t.Fatal("retirement changed task state")
			}
		})
	}
}
