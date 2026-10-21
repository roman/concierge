package conciergecli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestTaskReopenFollowUp(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	t.Setenv(workerLaunchGeneration, "gen-1")
	root, messagesRoot, _ := taskSupervisionFixture(t)
	store, _ := taskstate.NewStore(root)
	if _, err := store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Definition.Kind = "scout"
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	serveTaskSupervision(t, root, messagesRoot, io.Discard)
	messages, _ := inbox.New(messagesRoot)
	worker, _ := inbox.ParseRecipient("task-1")
	primary, _ := inbox.ParseRecipient("concierge")
	send := func(key string) string {
		invoke(
			t,
			root,
			"Question",
			"--message-root",
			messagesRoot,
			"task",
			"send",
			"task-1",
			"--key",
			key,
			"--body-file",
			"-",
		)
		pending, err := messages.List(worker)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending: %v %v", pending, err)
		}
		return pending[0].ID.String()
	}
	reply := func(assignment, key string) string {
		invoke(
			t,
			root,
			"Answer",
			"--message-root",
			messagesRoot,
			"task",
			"reply",
			"send",
			"task-1",
			"--generation",
			"gen-1",
			"--assignment-id",
			assignment,
			"--key",
			key,
			"--body-file",
			"-",
		)
		pending, err := messages.List(primary)
		if err != nil || len(pending) != 1 {
			t.Fatalf("replies: %v %v", pending, err)
		}
		return pending[0].ID.String()
	}
	firstReply := reply(send("initial"), "initial-result")
	invoke(
		t,
		root,
		"",
		"--message-root",
		messagesRoot,
		"backlog",
		"resolve",
		firstReply,
		"--mode",
		"close",
		"--closed-on",
		"2026-10-20",
	)
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{
			"--root",
			root,
			"--message-root",
			messagesRoot,
			"task",
			"send",
			"task-1",
			"--body-file",
			"-",
			"--key",
			"follow-up",
		},
		strings.NewReader("Question"),
		&output,
		io.Discard,
	)
	before := output.String()
	if code != 1 {
		t.Fatalf("closed task send: code %d %s", code, before)
	}
	if !strings.Contains(before, "invalid task transition") {
		t.Fatal(before)
	}
	t.Log("before: task send: invalid task transition")
	got := invoke(t, root, "", "task", "reopen", "task-1")
	if !strings.Contains(got, "status: reopened") ||
		!strings.Contains(got, "generation: \"gen-1\"") {
		t.Fatal(got)
	}
	assignment := send("follow-up")
	got = invoke(
		t,
		root,
		"",
		"--message-root",
		messagesRoot,
		"task",
		"receive",
		"task-1",
		assignment,
		"--generation",
		"gen-1",
	)
	if !strings.Contains(got, "status: current") {
		t.Fatal(got)
	}
	followUpReply := reply(assignment, "follow-up-result")
	if report := invoke(t, root, "", "task", "report", "task-1", followUpReply, "--format", "body"); report != "Answer" {
		t.Fatalf("report: %q", report)
	}
	got = invoke(t, root, "", "backlog", "show", "task-1")
	if !strings.Contains(got, "reopen_count: 1") ||
		!strings.Contains(got, "operator requested follow-up") {
		t.Fatal(got)
	}
	t.Log("after: reopen -> send -> receive current -> reply -> saved report: success")
}

func TestTaskReopenRequiresHostAdmission(t *testing.T) {
	for _, mode := range []string{"missing", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			root := shortTaskRoot(t)
			seedViewTask(t, root, "task-1", taskstate.Queued, nil, nil)
			if mode == "unauthorized" {
				serveTaskRoot(t, root)
			}
			got := expectLifecycleError(t, root, 1, "task", "reopen", "task-1")
			if strings.Contains(got, "unknown") {
				t.Fatal(got)
			}
		})
	}
}
