package workerservice

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

func dispatchFixture(t *testing.T) (*Server, *taskstate.Store, *inbox.Store, Request) {
	t.Helper()
	ctx, root := context.Background(), t.TempDir()
	if err := taskstate.Initialize(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := taskstate.UpgradeVersionTwo(ctx, root, root+"-backup"); err != nil {
		t.Fatal(err)
	}
	store, _ := taskstate.NewStore(root)
	task, _ := taskstate.NewQueued(
		taskstate.Definition{ID: "task-1", Title: "Work", Repo: "repo", Kind: "task"},
	)
	if err := store.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveAdoption(ctx, "task-1", taskstate.Worker{
		Machine: "dev-remote", Name: "worker", Harness: "codex", Session: "work", Pane: "pane-1",
		Project: "/remote/repo", Worktree: "/remote/worktree", Generation: "gen-1",
		AgentArgs: []string{"resume", "thread"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "task-1", func(current taskstate.Task) (taskstate.Task, error) {
		current, err := current.ConfirmAdoption("workspace-1", "tab-1")
		if err != nil {
			return taskstate.Task{}, err
		}
		current, err = current.Start()
		if err != nil {
			return taskstate.Task{}, err
		}
		return current.InstallWorkerAccess(grant)
	}); err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	server, err := New(
		Config{Root: root, MessageRoot: filepath.Join(root, "messages"), Recipient: recipient,
			ReadSettings: func(repo string) (taskops.WorkerSettings, error) {
				return taskops.WorkerSettings{Repo: repo, Merge: "off", Delivery: "pr"}, nil
			}},
	)
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	worker, _ := inbox.ParseRecipient("task-1")
	body, _ := json.Marshal(
		taskprotocol.Assignment{Kind: "task-assignment", State: filepath.Join(root, "tasks"),
			Task: "task-1", SpawnGeneration: "gen-1", Body: "Complete the work"},
	)
	message, err := messages.Send(worker, "assignment", string(body))
	if err != nil {
		t.Fatal(err)
	}
	return server, store, messages, Request{Task: "task-1", Generation: "gen-1",
		Secret: string(
			secret.Bytes(),
		), Operation: workeraccess.Receive, MessageID: message.ID.String()}
}

func TestWorkerReplyRetainsPublicationWhenArchiveFails(t *testing.T) {
	server, _, messages, request := dispatchFixture(t)
	attachment, _ := taskprotocol.NewAttachment("evidence.bin", []byte{0, 255})
	request.Attachments = []taskprotocol.Attachment{attachment}
	request.Operation, request.Key, request.Body = workeraccess.SendReply, "result", "Completed"
	archive := filepath.Join(server.config.Root, "reports")
	if err := os.WriteFile(archive, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	first := server.perform(context.Background(), request)
	if first.Outcome != Uncertain || first.Reply == nil ||
		first.Reply.Publication != taskops.ReplyConfirmed || first.Reply.Archive != taskops.ReplyUnconfirmed ||
		first.Reply.Assignment != taskops.ReplyAcknowledgementUnconfirmed {
		t.Fatalf("partial reply lost its independent outcomes: %+v", first)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	retried := server.perform(context.Background(), request)
	if retried.Outcome != Completed || retried.Reply == nil ||
		retried.Reply.MessageID != first.Reply.MessageID ||
		retried.Reply.Publication != taskops.ReplyConfirmed ||
		retried.Reply.Archive != taskops.ReplyConfirmed ||
		retried.Reply.Assignment != taskops.ReplyAcknowledged {
		t.Fatalf("reply recovery failed: %+v", retried)
	}
	pending, err := messages.List(server.config.Recipient)
	if err != nil || len(pending) != 1 {
		t.Fatalf(
			"partial archive recovery duplicated publication: count=%d error=%v",
			len(pending),
			err,
		)
	}
}

func TestWorkerDispatchDenialHasNoMutation(t *testing.T) {
	for _, trial := range []string{"secret", "task", "generation", "revoked", "primary operation"} {
		t.Run(trial, func(t *testing.T) {
			server, store, messages, request := dispatchFixture(t)
			attachment, _ := taskprotocol.NewAttachment("evidence.bin", []byte{0, 255})
			request.Attachments = []taskprotocol.Attachment{attachment}
			ctx := context.Background()
			request.Operation, request.Key, request.Body = workeraccess.SendReply, "result", "Done"
			want := Denied
			switch trial {
			case "secret":
				request.Secret = "wrong"
			case "task":
				request.Task = "other-task"
			case "generation":
				request.Generation = "gen-other"
			case "revoked":
				if _, err := store.Update(ctx, "task-1", func(current taskstate.Task) (taskstate.Task, error) {
					return current.RevokeWorkerAccess("gen-1")
				}); err != nil {
					t.Fatal(err)
				}
			case "primary operation":
				request.Operation, want = "land", Invalid
			}
			before, _ := store.Read(ctx, "task-1")
			beforeBytes, _ := before.Encode()
			beforePaths := taskRootPaths(t, server.config.Root)
			response := server.perform(ctx, request)
			if response.Outcome != want || response.Reply != nil {
				t.Fatalf("denied dispatch: %+v", response)
			}
			after, _ := store.Read(ctx, "task-1")
			afterBytes, _ := after.Encode()
			if !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("denied dispatch changed the task")
			}
			if !slices.Equal(beforePaths, taskRootPaths(t, server.config.Root)) {
				t.Fatal("denied dispatch created or removed filesystem paths")
			}
			pending, err := messages.List(server.config.Recipient)
			if err != nil || len(pending) != 0 {
				t.Fatalf("denied dispatch published a reply: %v", err)
			}
			worker, _ := inbox.ParseRecipient("task-1")
			pending, err = messages.List(worker)
			if err != nil || len(pending) != 1 {
				t.Fatalf("denied dispatch handled the assignment: %v", err)
			}
		})
	}
}

func taskRootPaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestWorkerDispatchReloadUsesPersistedVerifier(t *testing.T) {
	server, _, messages, request := dispatchFixture(t)
	ctx := context.Background()
	response := server.perform(ctx, request)
	if response.Outcome != Completed || response.Assignment == nil ||
		response.Assignment.Body != "Complete the work" {
		t.Fatalf("current receive: %+v", response)
	}
	reloaded, err := New(server.config)
	if err != nil {
		t.Fatal(err)
	}
	response = reloaded.perform(ctx, request)
	if response.Outcome != Completed || response.Assignment == nil ||
		response.Assignment.Status != taskops.AssignmentCurrent {
		t.Fatalf("receive after reload: %+v", response)
	}
	request.Operation, request.Key, request.Body = workeraccess.SendReply, "result", "Completed"
	first := reloaded.perform(ctx, request)
	if first.Outcome != Completed || first.Reply == nil ||
		first.Reply.Assignment != taskops.ReplyAcknowledged {
		t.Fatalf("reply: %+v", first)
	}
	retry := reloaded.perform(ctx, request)
	if retry.Outcome != Completed || retry.Reply == nil ||
		retry.Reply.MessageID != first.Reply.MessageID {
		t.Fatalf("identical retry: %+v", retry)
	}
	pending, err := messages.List(server.config.Recipient)
	if err != nil || len(pending) != 1 {
		t.Fatalf("reply was duplicated: count=%d error=%v", len(pending), err)
	}
}
