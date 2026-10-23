package taskops

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/tincan/pkg/inbox"
	"github.com/roman/tincan/pkg/notify"
)

func TestFreshFollowupWakesIdleWorkerWithOlderPendingAssignment(t *testing.T) {
	for _, state := range []herdr.AgentState{herdr.Idle, herdr.Done} {
		for _, episode := range []string{"expired", "alerted", "supervised"} {
			t.Run(string(state)+"/"+episode, func(t *testing.T) {
				service, runner, root := runningWorkerService(t)
				worker, err := service.SupervisedWorker(
					"task-1",
					root,
					herdr.Client{SessionRunner: runner},
				)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				worker.now = func() time.Time { return now }
				send := func(recipient inbox.Recipient, key, body string) (inbox.Message, error) {
					message, err := worker.messages.Send(recipient, key, body)
					if err != nil {
						return message, err
					}
					message.CreatedAt = now
					writeMessageFixture(t, root, "pending", message)
					return message, nil
				}
				runner.agentStatus = herdr.Working
				original, err := worker.sendAssignment(
					context.Background(),
					"original",
					"Do work",
					send,
				)
				if err != nil || original.Notification != NotificationDeferred {
					t.Fatalf("original assignment = %+v, %v", original, err)
				}
				// The worker has worked past the notification deadline and needs an answer.
				now = now.Add(stallDeadline + time.Second)
				runner.agentStatus = state
				if episode == "alerted" {
					if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
						t.Fatal(err)
					}
				}
				if episode == "supervised" {
					runner.agentStatus = herdr.Working
				}
				followup, err := worker.sendAssignment(
					context.Background(),
					"answer",
					"Continue with this answer",
					send,
				)
				if episode == "supervised" {
					if err != nil || followup.Notification != NotificationDeferred ||
						runner.prompts != 0 {
						t.Fatalf(
							"busy follow-up = %+v, %v; prompts=%d",
							followup,
							err,
							runner.prompts,
						)
					}
					runner.agentStatus = state
					worker, err = service.SupervisedWorker(
						"task-1",
						root,
						herdr.Client{SessionRunner: runner},
					)
					if err != nil {
						t.Fatal(err)
					}
					worker.now = func() time.Time { return now }
					if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
						t.Fatal(err)
					}
				} else if err != nil || followup.Notification != NotificationSent {
					t.Fatalf("fresh follow-up = %+v, %v", followup, err)
				}
				duplicate, err := worker.sendAssignment(
					context.Background(),
					"answer",
					"Continue with this answer",
					worker.messages.Send,
				)
				if err != nil || duplicate.Message.ID != followup.Message.ID ||
					duplicate.Notification != NotificationDeferred ||
					runner.prompts != 1 {
					t.Fatalf(
						"duplicate follow-up = %+v, %v; prompts=%d",
						duplicate,
						err,
						runner.prompts,
					)
				}
				current, err := service.store.Read(context.Background(), "task-1")
				if err != nil {
					t.Fatal(err)
				}
				progress := current.Snapshot().Worker.PromptProgress
				if progress.AssignmentID != followup.Message.ID.String() ||
					progress.Attempts != 1 ||
					progress.Alert != nil {
					t.Fatalf("follow-up inherited original budget: %+v", progress)
				}
				if _, err := worker.messages.Read(worker.recipient, original.Message.ID); err != nil {
					t.Fatalf("original assignment changed: %v", err)
				}
			})
		}
	}
}

func TestStaleFollowupDoesNotReleaseCurrentGenerationStall(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	runner.agentStatus = herdr.Done
	worker, err := service.SupervisedWorker("task-1", root, herdr.Client{SessionRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	original, err := worker.sendAssignment(
		context.Background(),
		"original",
		"Do work",
		worker.messages.Send,
	)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return original.Message.CreatedAt.Add(stallDeadline) }
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(taskprotocol.Assignment{
		Kind: "task-assignment", State: filepath.Join(service.root, "tasks"),
		Task: "task-1", SpawnGeneration: "old-generation", Body: "Stale answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := worker.messages.Send(worker.recipient, "stale", string(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	current, err := service.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	progress := current.Snapshot().Worker.PromptProgress
	if runner.prompts != 1 || progress.AssignmentID != original.Message.ID.String() ||
		progress.Alert == nil {
		t.Fatalf("stale follow-up released stall: %+v; prompts=%d", progress, runner.prompts)
	}
	if _, err := worker.messages.Read(worker.recipient, stale.ID); err != nil {
		t.Fatalf("stale follow-up changed: %v", err)
	}
}
