package taskops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
	"github.com/roman/tincan/pkg/notify"
)

func TestClaudeBackgroundShellSummary(t *testing.T) {
	for _, test := range []struct {
		name   string
		screen string
		want   bool
	}{
		{"one shell", "✻ Cogitated for 4s · done 12:28 AM · 1 shell still running\n\n────────\n❯ \n────────", true},
		{"multiple shells", "✽ Worked for 1m · 12 shells still running\n\n────────\n❯ \n────────", true},
		{"zero shells", "✻ Worked for 4s · 0 shells still running\n\n────────\n❯ \n────────", false},
		{"tool output", "● 1 shell still running\n\n────────\n❯ \n────────", false},
		{"historical summary", "✻ Worked for 4s · 1 shell still running\n● Native error\n\n────────\n❯ \n────────", false},
		{"no current input", "✻ Worked for 4s · 1 shell still running", false},
		{"user message", "✻ Worked for 4s · 1 shell still running\n❯ Quote the above", false},
		{"completed turn", "✻ Worked for 4s · 1 shell still running\n❯ Next message\n● Done\n✻ Worked for 1s\n\n────────\n❯ \n────────", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := claudeBackgroundShellRunning(test.screen); got != test.want {
				t.Fatalf("background shell = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClaudeBackgroundShellDefersPromptsUntilItFinishes(t *testing.T) {
	for _, state := range []herdr.AgentState{herdr.Done, herdr.Idle} {
		t.Run(string(state), func(t *testing.T) {
			service, runner, root := runningWorkerService(t)
			runner.harness, runner.agentStatus = "claude", state
			_, err := service.store.Update(
				context.Background(),
				"task-1",
				func(current taskstate.Task) (taskstate.Task, error) {
					snapshot := current.Snapshot()
					snapshot.Worker.Harness = "claude"
					return taskstate.NewMigrated(snapshot)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			runner.screen = "● Waiting.\n\n✻ Cogitated for 4s · done 12:28 AM · 1 shell still running\n\n────────\n❯ \n────────\n"
			worker, err := service.SupervisedWorker(
				"task-1",
				root,
				herdr.Client{SessionRunner: runner},
			)
			if err != nil {
				t.Fatal(err)
			}
			first, err := worker.sendAssignment(
				context.Background(),
				"work",
				"Do work",
				worker.messages.Send,
			)
			if err != nil || first.Notification != NotificationDeferred || runner.prompts != 0 {
				t.Fatalf(
					"background wait prompted: %+v, %v; prompts=%d",
					first,
					err,
					runner.prompts,
				)
			}
			worker.now = func() time.Time { return first.Message.CreatedAt.Add(stallDeadline + time.Minute) }
			if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
				t.Fatal(err)
			}
			current, err := service.store.Read(context.Background(), "task-1")
			if err != nil {
				t.Fatal(err)
			}
			progress := current.Snapshot().Worker.PromptProgress
			if progress.Attempts != 0 || progress.Alert != nil ||
				!progress.StartedAt.Equal(first.Message.CreatedAt) {
				t.Fatalf("background wait changed stall budget: %+v", progress)
			}
			// Historical shell text must not hide a later idle worker.
			runner.screen += "\n● Background command completed\n✻ Cogitated for 2s · done 12:29 AM\n\n────────\n❯ \n────────\n"
			if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
				t.Fatal(err)
			}
			notices, err := worker.messages.List(service.primaryRecipient)
			if err != nil || len(notices) != 1 || runner.prompts != 0 {
				t.Fatalf(
					"finished shell hid a stall: %+v, %v; prompts=%d",
					notices,
					err,
					runner.prompts,
				)
			}
		})
	}
}

func TestBackgroundShellTextDoesNotHideOtherStalls(t *testing.T) {
	for _, test := range []struct {
		name      string
		harness   string
		state     herdr.AgentState
		readError error
	}{
		{"codex", "codex", herdr.Done, nil},
		{"blocked claude", "claude", herdr.Blocked, nil},
		{"unreadable claude", "claude", herdr.Done, errors.New("screen unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runner, root := runningWorkerService(t)
			runner.harness, runner.agentStatus, runner.screenError = test.harness, test.state, test.readError
			_, err := service.store.Update(
				context.Background(),
				"task-1",
				func(current taskstate.Task) (taskstate.Task, error) {
					snapshot := current.Snapshot()
					snapshot.Worker.Harness = test.harness
					return taskstate.NewMigrated(snapshot)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			runner.screen = "✻ Cogitated for 4s · 1 shell still running\n\n────────\n❯ \n────────\n"
			worker, err := service.SupervisedWorker(
				"task-1",
				root,
				herdr.Client{SessionRunner: runner},
			)
			if err != nil {
				t.Fatal(err)
			}
			first, err := worker.sendAssignment(
				context.Background(),
				"work",
				"Do work",
				worker.messages.Send,
			)
			if err != nil {
				t.Fatal(err)
			}
			worker.now = func() time.Time { return first.Message.CreatedAt.Add(stallDeadline) }
			if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
				t.Fatal(err)
			}
			notices, err := worker.messages.List(service.primaryRecipient)
			if err != nil || len(notices) != 1 {
				t.Fatalf("stall hidden: %+v, %v", notices, err)
			}
		})
	}
}

func TestStallNoticeExplainsRemoteCodexApprovalReviewer(t *testing.T) {
	for _, test := range []struct {
		name    string
		machine string
		harness string
		want    bool
	}{
		{"remote Codex", "baker", "codex", true},
		{"local Codex", "", "codex", false},
		{"remote Claude", "baker", "claude", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runner, messageRoot := runningWorkerService(t)
			worker, err := service.SupervisedWorker(
				"task-1", messageRoot, herdr.Client{SessionRunner: runner},
			)
			if err != nil {
				t.Fatal(err)
			}
			bound := taskstate.Worker{
				Machine: test.machine, Harness: test.harness, Generation: "gen-1",
				PromptProgress: &taskstate.PromptProgress{Alert: &taskstate.StallAlert{
					State: herdr.Blocked, Screen: "Permission required",
				}},
			}
			if _, err := worker.publishStallAlert(bound); err != nil {
				t.Fatal(err)
			}
			notices, err := worker.messages.List(service.primaryRecipient)
			if err != nil || len(notices) != 1 {
				t.Fatalf("stall notices = %+v, %v", notices, err)
			}
			var notice struct {
				Help string `json:"help"`
			}
			if err := json.Unmarshal([]byte(notices[0].Body), &notice); err != nil {
				t.Fatal(err)
			}
			for _, advice := range []string{
				"remote host", "~/.codex/config.toml", `approvals_reviewer = "auto_review"`,
				"--codex-approval-reviewer=auto-review", "--codex-approval-reviewer=native", "Never answer",
			} {
				if strings.Contains(notice.Help, advice) != test.want {
					t.Errorf("help contains %q: want %t; help=%s", advice, test.want, notice.Help)
				}
			}
		})
	}
}

func TestStallNoticePreservesPublishedBodyAcrossUpgrade(t *testing.T) {
	for _, disposition := range []string{"pending", "handled"} {
		for _, change := range []string{"help", "pane"} {
			t.Run(disposition+"/"+change, func(t *testing.T) {
				service, runner, root := runningWorkerService(t)
				worker, err := service.SupervisedWorker(
					"task-1",
					root,
					herdr.Client{SessionRunner: runner},
				)
				if err != nil {
					t.Fatal(err)
				}
				bound := taskstate.Worker{
					Harness: "codex", Generation: "gen-1", Pane: "%1",
					PromptProgress: &taskstate.PromptProgress{
						AssignmentID: "assignment-1",
						Alert: &taskstate.StallAlert{
							State:  herdr.Blocked,
							Screen: "Approval required",
						},
					},
				}
				if _, err := worker.publishStallAlert(bound); err != nil {
					t.Fatal(err)
				}
				notices, err := worker.messages.List(service.primaryRecipient)
				if err != nil || len(notices) != 1 {
					t.Fatalf("initial notices = %+v, %v", notices, err)
				}
				original := notices[0]
				if disposition == "handled" {
					if err := worker.messages.Acknowledge(service.primaryRecipient, original.ID); err != nil {
						t.Fatal(err)
					}
				}
				if change == "help" {
					// Remote Codex advice changes the derived body without changing its key.
					bound.Machine = "baker"
				} else {
					bound.Pane = "%2"
				}
				replacement, err := service.SupervisedWorker(
					"task-1",
					root,
					herdr.Client{SessionRunner: runner},
				)
				if err != nil {
					t.Fatal(err)
				}
				for attempt := 0; attempt < 2; attempt++ {
					status, err := replacement.publishStallAlert(bound)
					if err != nil || status != NotificationDeferred {
						t.Fatalf("upgrade retry = %s, %v", status, err)
					}
				}
				notices, err = worker.messages.List(service.primaryRecipient)
				if err != nil {
					t.Fatal(err)
				}
				var saved inbox.Message
				if disposition == "handled" {
					if len(notices) != 0 {
						t.Fatalf("republished handled notice: %+v", notices)
					}
					saved, err = worker.messages.ReadHandled(service.primaryRecipient, original.ID)
				} else {
					if len(notices) != 1 {
						t.Fatalf("retry notices = %+v", notices)
					}
					saved = notices[0]
				}
				if err != nil || saved.ID != original.ID || saved.Body != original.Body {
					t.Fatalf("retry changed the published notice: %+v, %v", saved, err)
				}
			})
		}
	}
}

func TestStalledAssignmentBoundsPromptsAndAlertsPrimary(t *testing.T) {
	service, runner, messageRoot := runningWorkerService(t)
	runner.agentStatus = herdr.Done
	runner.screen = "You're out of usage credits. Run /usage-credits or /model."
	worker, err := service.SupervisedWorker(
		"task-1",
		messageRoot,
		herdr.Client{SessionRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	now := started
	worker.now = func() time.Time { return now }
	result, err := worker.sendAssignment(
		context.Background(),
		"work",
		"Do work",
		func(recipient inbox.Recipient, key, body string) (inbox.Message, error) {
			message, err := worker.messages.Send(recipient, key, body)
			started, now = message.CreatedAt, message.CreatedAt
			return message, err
		},
	)
	if err != nil || result.Notification != NotificationSent || runner.prompts != 1 {
		t.Fatalf("first prompt = %+v, %v; prompts=%d", result, err, runner.prompts)
	}
	for _, step := range []struct {
		elapsed time.Duration
		prompts int
	}{
		{0, 1}, {4 * time.Second, 1}, {5 * time.Second, 2}, {14 * time.Second, 2}, {15 * time.Second, 3}, {time.Minute, 3}, {5*time.Minute - time.Nanosecond, 3}, {5 * time.Minute, 3},
	} {
		now = started.Add(step.elapsed)
		if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
			t.Fatal(err)
		}
		if runner.prompts != step.prompts {
			t.Fatalf("after %s: prompts=%d, want %d", step.elapsed, runner.prompts, step.prompts)
		}
	}
	notices, err := worker.messages.List(service.primaryRecipient)
	if err != nil || len(notices) != 1 {
		t.Fatalf("primary stall notices = %+v, %v", notices, err)
	}
	var notice struct {
		Kind       string `json:"kind"`
		Task       string `json:"task"`
		Generation string `json:"generation"`
		Assignment string `json:"assignment_id"`
		State      string `json:"observed_state"`
		Screen     string `json:"observed_screen"`
	}
	if err := json.Unmarshal([]byte(notices[0].Body), &notice); err != nil {
		t.Fatal(err)
	}
	if notice.Kind != "task-stalled" || notice.Task != "task-1" || notice.Generation != "gen-1" ||
		notice.Assignment != result.Message.ID.String() ||
		notice.State != "done" ||
		!strings.Contains(notice.Screen, "out of usage credits") {
		t.Fatalf("stall notice = %+v", notice)
	}
	// Reconstructing a controller must preserve its prompt budget and frozen notice.
	replacement, err := service.SupervisedWorker(
		"task-1",
		messageRoot,
		herdr.Client{SessionRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement.now = func() time.Time { return now.Add(time.Minute) }
	runner.screen = "The screen changed after the alert."
	if err := replacement.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	retried, err := worker.messages.List(service.primaryRecipient)
	if err != nil || len(retried) != 1 || retried[0].ID != notices[0].ID ||
		retried[0].Body != notices[0].Body ||
		runner.prompts != 3 {
		t.Fatalf(
			"controller restart reset the stall episode: %+v, %v; prompts=%d",
			retried,
			err,
			runner.prompts,
		)
	}
}

func TestAssignmentRetryDefersAnIneffectivePrompt(t *testing.T) {
	service, runner, messageRoot := runningWorkerService(t)
	runner.agentStatus = herdr.Done
	worker, err := service.SupervisedWorker(
		"task-1",
		messageRoot,
		herdr.Client{SessionRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	worker.now = func() time.Time { return now }
	first, err := worker.sendAssignment(
		context.Background(),
		"work",
		"Do work",
		worker.messages.Send,
	)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := worker.sendAssignment(
		context.Background(),
		"work",
		"Do work",
		worker.messages.Send,
	)
	if err != nil || retry.Message.ID != first.Message.ID ||
		retry.Notification != NotificationDeferred ||
		runner.prompts != 1 {
		t.Fatalf(
			"key retry repeated an ineffective prompt: %+v, %v; prompts=%d",
			retry,
			err,
			runner.prompts,
		)
	}
}

func TestStallDeadlineUsesPublishedAssignmentTime(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	runner.agentStatus = herdr.Done
	worker, err := service.SupervisedWorker("task-1", root, herdr.Client{SessionRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a controller stopping after publication but before recording a prompt.
	_, err = worker.sendAssignment(
		context.Background(),
		"old",
		"Do work",
		func(recipient inbox.Recipient, key, body string) (inbox.Message, error) {
			message, err := worker.messages.Send(recipient, key, body)
			if err != nil {
				return message, err
			}
			return message, context.Canceled
		},
	)
	if err == nil {
		t.Fatal("publication interruption succeeded")
	}
	messages, err := worker.messages.List(worker.recipient)
	if err != nil || len(messages) != 1 {
		t.Fatalf("pending: %+v, %v", messages, err)
	}
	now := messages[0].CreatedAt.Add(stallDeadline - time.Second)
	worker.now = func() time.Time { return now }
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	notices, err := worker.messages.List(service.primaryRecipient)
	if err != nil || len(notices) != 1 || runner.prompts != 1 {
		t.Fatalf(
			"deadline restarted after publication: notices=%+v, %v; prompts=%d",
			notices,
			err,
			runner.prompts,
		)
	}
}

func TestWorkingAssignmentDefersWithoutConsumingPromptBudget(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	runner.agentStatus = herdr.Working
	result, err := service.SendAssignment(
		context.Background(),
		root,
		"task-1",
		"work",
		"Do work",
		herdr.Client{SessionRunner: runner},
	)
	if err != nil || result.Notification != NotificationDeferred || runner.prompts != 0 {
		t.Fatalf("busy assignment: %+v, %v; prompts=%d", result, err, runner.prompts)
	}
	runner.agentStatus = herdr.Idle
	retry, err := service.SendAssignment(
		context.Background(),
		root,
		"task-1",
		"work",
		"Do work",
		herdr.Client{SessionRunner: runner},
	)
	if err != nil || retry.Notification != NotificationSent || runner.prompts != 1 {
		t.Fatalf("ready assignment: %+v, %v; prompts=%d", retry, err, runner.prompts)
	}
}

func TestHandledAssignmentClearsPromptBudgetImmediately(t *testing.T) {
	for _, operation := range []string{"ack", "reply"} {
		t.Run(operation, func(t *testing.T) {
			service, runner, root := runningWorkerService(t)
			result, err := service.SendAssignment(
				context.Background(),
				root,
				"task-1",
				"work",
				"Do work",
				herdr.Client{SessionRunner: runner},
			)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "ack" {
				_, err = service.AcknowledgeAssignment(
					context.Background(),
					root,
					"task-1",
					result.Message.ID.String(),
					"gen-1",
				)
			} else {
				_, err = service.SendReply(context.Background(), root, ReplyInput{TaskID: "task-1", Generation: "gen-1", Key: "reply", AssignmentID: result.Message.ID.String(), Body: "Done"})
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err := service.store.Read(context.Background(), "task-1")
			if err != nil || current.Snapshot().Worker.PromptProgress != nil {
				t.Fatalf("handled assignment retained budget: %+v, %v", current.Snapshot(), err)
			}
		})
	}
}

func TestStallAlertRetriesFrozenEvidenceAfterPublicationFailure(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	runner.agentStatus = herdr.Done
	runner.screen = "Original native error"
	worker, err := service.SupervisedWorker("task-1", root, herdr.Client{SessionRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	first, err := worker.sendAssignment(
		context.Background(),
		"work",
		"Do work",
		worker.messages.Send,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := first.Message.CreatedAt.Add(stallDeadline)
	worker.now = func() time.Time { return now }
	primary := filepath.Join(root, "inbox", service.primaryRecipient.String())
	if err := os.MkdirAll(primary, 0700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(primary, "handled")
	if err := os.WriteFile(blocker, []byte("block send"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err == nil {
		t.Fatal("publication failure succeeded")
	}
	saved, err := service.store.Read(context.Background(), "task-1")
	if err != nil || saved.Snapshot().Worker.PromptProgress.Alert == nil {
		t.Fatalf("lost alert after send failure: %+v, %v", saved.Snapshot(), err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	runner.screen = "Changed error"
	now = now.Add(time.Minute)
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	notices, err := worker.messages.List(service.primaryRecipient)
	if err != nil || len(notices) != 1 ||
		!strings.Contains(notices[0].Body, "Original native error") ||
		strings.Contains(notices[0].Body, "Changed error") ||
		runner.prompts != 1 {
		t.Fatalf("retry changed frozen evidence: %+v, %v; prompts=%d", notices, err, runner.prompts)
	}
}

func TestBlockedAssignmentAlertsWithoutPromptAttempt(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	runner.agentStatus = herdr.Blocked
	runner.screen = "Native approval required"
	worker, err := service.SupervisedWorker("task-1", root, herdr.Client{SessionRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	first, err := worker.sendAssignment(
		context.Background(),
		"work",
		"Do work",
		worker.messages.Send,
	)
	if err != nil || first.Notification != NotificationDeferred {
		t.Fatalf("blocked send: %+v, %v", first, err)
	}
	worker.now = func() time.Time { return first.Message.CreatedAt.Add(stallDeadline) }
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	notices, err := worker.messages.List(service.primaryRecipient)
	if err != nil || len(notices) != 1 || runner.prompts != 0 {
		t.Fatalf(
			"blocked assignment never alerted: %+v, %v; prompts=%d",
			notices,
			err,
			runner.prompts,
		)
	}
}

func TestConfirmedReplyClearsBudgetWhenAcknowledgementFails(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	first, err := service.SendAssignment(
		context.Background(),
		root,
		"task-1",
		"work",
		"Do work",
		herdr.Client{SessionRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	handled := filepath.Join(root, "inbox", "task-1", "handled")
	if err := os.Rename(handled, handled+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handled, []byte("block acknowledgement"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := service.SendReply(
		context.Background(),
		root,
		ReplyInput{
			TaskID:       "task-1",
			Generation:   "gen-1",
			Key:          "reply",
			AssignmentID: first.Message.ID.String(),
			Body:         "Done",
		},
	)
	if err == nil || result.Message.ID.String() == "" ||
		result.Assignment != ReplyAcknowledgementUnconfirmed {
		t.Fatalf("ack failure: %+v, %v", result, err)
	}
	current, err := service.store.Read(context.Background(), "task-1")
	if err != nil || current.Snapshot().Worker.PromptProgress != nil {
		t.Fatalf("confirmed reply retained budget: %+v, %v", current.Snapshot(), err)
	}
}

func TestConfirmedReplyRetryClearsProgressAfterCompletion(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	first, err := service.SendAssignment(
		context.Background(),
		root,
		"task-1",
		"work",
		"Do work",
		herdr.Client{SessionRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	input := ReplyInput{
		TaskID:       "task-1",
		Generation:   "gen-1",
		Key:          "reply",
		AssignmentID: first.Message.ID.String(),
		Body:         "Done",
	}
	reply, err := service.SendReply(context.Background(), root, input)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the durable state left by a failed progress write after reply publication.
	if _, err := service.store.Update(context.Background(), "task-1", func(current taskstate.Task) (taskstate.Task, error) {
		snapshot := current.Snapshot()
		snapshot.Worker.PromptProgress = before.Snapshot().Worker.PromptProgress
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finish(context.Background(), "task-1", "gen-1", taskstate.Close, "2026-10-13"); err != nil {
		t.Fatal(err)
	}
	retry, err := service.SendReply(context.Background(), root, input)
	if err != nil || retry.Message.ID != reply.Message.ID || retry.Assignment != ReplyAcknowledged {
		t.Fatalf("completed reply retry: %+v, %v", retry, err)
	}
}

func TestFreshAssignmentGetsOwnBudgetWhileOlderAssignmentRemainsPending(t *testing.T) {
	service, runner, root := runningWorkerService(t)
	worker, err := service.SupervisedWorker("task-1", root, herdr.Client{SessionRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	worker.now = func() time.Time { return now }
	first, err := worker.sendAssignment(
		context.Background(),
		"first",
		"First work",
		worker.messages.Send,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.sendAssignment(
		context.Background(),
		"second",
		"Second work",
		worker.messages.Send,
	)
	if err != nil || second.Notification != NotificationSent || runner.prompts != 2 {
		t.Fatalf(
			"new assignment did not get its own budget: %+v, %v; prompts=%d",
			second,
			err,
			runner.prompts,
		)
	}
	current, err := service.store.Read(context.Background(), "task-1")
	if err != nil ||
		current.Snapshot().Worker.PromptProgress.AssignmentID != second.Message.ID.String() {
		t.Fatalf("freshest selection: %+v, %v", current.Snapshot(), err)
	}
	if _, err := service.AcknowledgeAssignment(context.Background(), root, "task-1", first.Message.ID.String(), "gen-1"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Prompt(context.Background(), "task-1", notify.DoorbellText); err != nil {
		t.Fatal(err)
	}
	current, err = service.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	progress := current.Snapshot().Worker.PromptProgress
	if progress.AssignmentID != second.Message.ID.String() || progress.Attempts != 1 ||
		!progress.StartedAt.Equal(second.Message.CreatedAt) ||
		runner.prompts != 2 {
		t.Fatalf(
			"second assignment inherited old budget: %+v; prompts=%d",
			progress,
			runner.prompts,
		)
	}
}
