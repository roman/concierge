package hostservice

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestCleanupRetiresPrimaryWakes(t *testing.T) {
	for _, mode := range []taskstate.CompletionMode{taskstate.Close, taskstate.Retain} {
		t.Run(string(mode), func(t *testing.T) {
			server, _, _ := taskEndpointFixture(t)
			closedOn := "2026-10-22"
			if mode == taskstate.Retain {
				closedOn = ""
				_, err := server.store.Update(
					t.Context(),
					"task-1",
					func(task taskstate.Task) (taskstate.Task, error) {
						return task.PlaceHold(taskstate.Hold{Reason: "More work"})
					},
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := server.service.Finish(t.Context(), "task-1", "gen-1", mode, closedOn); err != nil {
				t.Fatal(err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			var wakes []inbox.Message
			for _, key := range []string{
				fmt.Sprintf("task-stalled:%s:task-1:gen-1:assignment", server.config.Root),
				fmt.Sprintf("task-restart:%s:task-1:gen-1", server.config.Root),
				fmt.Sprintf("task-native-checkpoint:%s:task-1:gen-1:permission", server.config.Root),
			} {
				message, err := messages.Send(server.config.Recipient, key, "diagnostic")
				if err != nil {
					t.Fatal(err)
				}
				wakes = append(wakes, message)
			}
			request := Request{
				Operation:  Cleanup,
				Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
			}
			if _, err := server.execute(t.Context(), request, nil); err == nil {
				t.Fatal("cleanup succeeded without pane departure")
			}
			pending, err := messages.List(server.config.Recipient)
			if err != nil || len(pending) != len(wakes) {
				t.Fatalf("failed cleanup changed wakes: %v, %v", pending, err)
			}
			if err := os.WriteFile(server.config.HerdrExecutable, []byte("#!/bin/sh\nprintf '%s\\n' '{\"error\":{\"code\":\"pane_not_found\"}}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := server.execute(t.Context(), request, nil); err != nil {
					t.Fatal(err)
				}
				pending, err = messages.List(server.config.Recipient)
				if err != nil || len(pending) != 0 {
					t.Fatalf("cleanup left wakes pending: %v, %v", pending, err)
				}
			}
			for _, wake := range wakes {
				if _, err := messages.ReadHandled(server.config.Recipient, wake.ID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestHostStartupRetiresOldWakesBeforeReadiness(t *testing.T) {
	for _, policy := range []RecoveryPolicy{RecoveryNormal, RecoveryMaintenance} {
		t.Run(string(policy), func(t *testing.T) {
			server, _, _ := taskEndpointFixture(t)
			server.config.Recovery = policy
			if _, err := server.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
				snapshot := task.Snapshot()
				snapshot.Worker = nil
				snapshot.State = taskstate.Done
				snapshot.Definition.Closed = "2026-10-22"
				snapshot.Receipt = &taskstate.Receipt{Generation: "gen-1", Mode: taskstate.Close}
				return taskstate.NewMigrated(snapshot)
			}); err != nil {
				t.Fatal(err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			wake, err := messages.Send(server.config.Recipient,
				fmt.Sprintf("task-restart:%s:task-1:gen-1", server.config.Root), "old wake")
			if err != nil {
				t.Fatal(err)
			}
			directory, err := os.MkdirTemp("", "stale-wakes-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(directory)
			listener, err := net.ListenUnix(
				"unix",
				&net.UnixAddr{Name: filepath.Join(directory, "host.sock"), Net: "unix"},
			)
			if err != nil {
				t.Fatal(err)
			}
			ready := errors.New("readiness reached")
			err = server.ServeReady(t.Context(), listener, func() error {
				if _, err := messages.ReadHandled(server.config.Recipient, wake.ID); err != nil {
					t.Errorf("old wake remains pending at readiness: %v", err)
				}
				return ready
			})
			if !errors.Is(err, ready) {
				t.Fatal(err)
			}
		})
	}
}

func TestCleanupHistoricalReceiptKeepsReplacementWake(t *testing.T) {
	server, _, _ := taskEndpointFixture(t)
	if _, err := server.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Receipt = &taskstate.Receipt{Generation: "gen-old", Mode: taskstate.Retain}
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	for _, generation := range []string{"gen-old", "gen-1"} {
		if _, err := messages.Send(server.config.Recipient,
			fmt.Sprintf("task-restart:%s:task-1:%s", server.config.Root, generation), generation); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		Operation:  Cleanup,
		Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-old"},
	}
	if _, err := server.execute(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	pending, err := messages.List(server.config.Recipient)
	if err != nil || len(pending) != 1 || pending[0].Body != "gen-1" {
		t.Fatalf("replacement wake changed: %v, %v", pending, err)
	}
}

func TestCleanupRetriesWakeAcknowledgementAfterRetirement(t *testing.T) {
	server, _, _ := taskEndpointFixture(t)
	if _, err := server.service.Finish(t.Context(), "task-1", "gen-1", taskstate.Close, "2026-10-22"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server.config.HerdrExecutable, []byte("#!/bin/sh\nprintf '%s\\n' '{\"error\":{\"code\":\"pane_not_found\"}}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	wake, err := messages.Send(server.config.Recipient,
		fmt.Sprintf("task-restart:%s:task-1:gen-1", server.config.Root), "diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	handled := filepath.Join(
		server.config.MessageRoot,
		"inbox",
		server.config.Recipient.String(),
		"handled",
	)
	if err := os.Remove(handled); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handled, []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Operation:  Cleanup,
		Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
	}
	retired, err := server.execute(t.Context(), request, nil)
	if err == nil || !retired.HasReceipt("gen-1", taskstate.Close) {
		t.Fatalf("lost retirement or acknowledgement error: %v", err)
	}
	if _, err := messages.Read(server.config.Recipient, wake.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(handled); err != nil {
		t.Fatal(err)
	}
	if _, err := server.execute(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.ReadHandled(server.config.Recipient, wake.ID); err != nil {
		t.Fatal(err)
	}
}
