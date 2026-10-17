package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
	"github.com/roman/tincan/pkg/inbox"
)

func replyDeliveryEndpoint(t *testing.T) (string, *inbox.Store, inbox.Message, string) {
	t.Helper()
	parent, err := os.MkdirTemp("", "reply-delivery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	root := filepath.Join(parent, "primary")
	if err := taskstate.Initialize(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := taskstate.UpgradeVersionTwo(context.Background(), root, filepath.Join(parent, "backup")); err != nil {
		t.Fatal(err)
	}
	grant, secret, _ := workeraccess.Issue("task-1", "test-generation", workeraccess.Operations())
	task, err := taskstate.NewMigrated(taskstate.Snapshot{
		Definition: taskstate.Definition{
			ID:    "task-1",
			Title: "Remote reply test",
			Repo:  "repo",
			Kind:  "task",
		},
		State: taskstate.InFlight,
		Worker: &taskstate.Worker{
			Name:       "worker",
			Harness:    "codex",
			Machine:    "machine-1",
			Session:    "work",
			Pane:       "pane-1",
			Workspace:  "workspace-1",
			Tab:        "tab-1",
			Project:    filepath.Join(parent, "repo"),
			Worktree:   filepath.Join(parent, "worktree"),
			Generation: grant.Generation,
			AgentArgs:  []string{"resume", "thread-1"},
			Access:     &grant,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := task.Encode()
	if err := os.WriteFile(filepath.Join(root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	messageRoot := filepath.Join(parent, "messages")
	messages, _ := inbox.New(messageRoot)
	recipient, _ := inbox.ParseRecipient("task-1")
	body, _ := json.Marshal(
		taskprotocol.Assignment{Kind: "task-assignment", State: filepath.Join(root, "tasks"),
			Task: grant.Task, SpawnGeneration: grant.Generation, Body: "Complete the remote task"},
	)
	assignment, err := messages.Send(recipient, "assignment", string(body))
	if err != nil {
		t.Fatal(err)
	}
	primary, _ := inbox.ParseRecipient("concierge")
	server, err := workerservice.New(
		workerservice.Config{Root: root, MessageRoot: messageRoot, Recipient: primary},
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := workerservice.Listen(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker service did not stop")
		}
	})
	directory := filepath.Join(parent, "remote")
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := replyoutbox.InstallContext(directory, replyoutbox.Context{Format: 1, Root: root,
		Task: grant.Task, Generation: grant.Generation, Recipient: primary.String(), Verifier: grant.Verifier}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCIERGE_WORKER_TASK", grant.Task)
	t.Setenv(workerLaunchGeneration, grant.Generation)
	t.Setenv("CONCIERGE_WORKER_SECRET_FILE", filepath.Join(directory, "secret"))
	t.Setenv("CONCIERGE_WORKER_SOCKET", filepath.Join(directory, "worker.sock"))
	return root, messages, assignment, filepath.Join(root, "worker.sock")
}

func TestRemoteReplyDisconnectRetryPublishesOneReply(t *testing.T) {
	for _, drop := range []string{"before publication", "after publication"} {
		t.Run(drop, func(t *testing.T) {
			root, messages, assignment, endpoint := replyDeliveryEndpoint(t)
			listener, err := net.ListenUnix(
				"unix",
				&net.UnixAddr{Name: os.Getenv("CONCIERGE_WORKER_SOCKET"), Net: "unix"},
			)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for attempt := range 2 {
					connection, err := listener.AcceptUnix()
					if err != nil {
						return
					}
					func() {
						defer connection.Close()
						_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
						data, err := io.ReadAll(io.LimitReader(connection, 2<<20))
						if err != nil || attempt == 0 && drop == "before publication" {
							return
						}
						var request workerservice.Request
						if json.Unmarshal(data, &request) != nil {
							return
						}
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						response, _ := workerservice.Call(ctx, endpoint, request)
						if attempt == 0 {
							return
						}
						_ = json.NewEncoder(connection).Encode(response)
					}()
				}
			}()
			t.Cleanup(func() { _ = listener.Close(); <-done })
			id, body := assignment.ID.String(), "Completed across a disconnect\n"
			if code, output := runRemoteReply(t, id, body); code == 0 {
				t.Fatalf("dropped reply returned success: %s", output)
			}
			if record := retainedRemoteReply(t, id, body); record.Complete() {
				t.Fatal("disconnected client invented confirmation")
			}
			if code, output := runRemoteReply(t, id, body); code != 0 {
				t.Fatalf("identical retry failed: %s", output)
			}
			if record := retainedRemoteReply(t, id, body); !record.Complete() {
				t.Fatal("retry did not persist all delivery confirmations")
			}
			primary, _ := inbox.ParseRecipient("concierge")
			pending, err := messages.List(primary)
			if err != nil || len(pending) != 1 {
				t.Fatalf("reply publication count=%d error=%v", len(pending), err)
			}
			reports, _ := inbox.New(filepath.Join(root, "reports"))
			archived, err := reports.List(assignment.Recipient)
			if err != nil || len(archived) != 1 {
				t.Fatalf("reply archive count=%d error=%v", len(archived), err)
			}
			if _, err := messages.ReadHandled(assignment.Recipient, assignment.ID); err != nil {
				t.Fatalf("assignment acknowledgement is missing: %v", err)
			}
		})
	}
}
