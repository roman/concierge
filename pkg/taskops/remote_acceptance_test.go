package taskops

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestRemoteAcceptancePreparesBeforeFinishOrReplyResolution(t *testing.T) {
	for _, operation := range []string{"finish", "resolve"} {
		for _, mode := range []string{"complete", "offline", "invalid date", "disposition conflict", "stale reply"} {
			if operation == "finish" && (mode == "disposition conflict" || mode == "stale reply") {
				continue
			}
			t.Run(operation+"/"+mode, func(t *testing.T) {
				service, exporter, directory := remoteResultMaterializationFixture(t)
				ctx := context.Background()
				before, err := service.store.Read(ctx, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				date := "2026-10-17"
				if mode == "invalid date" {
					date = "invalid"
				}
				if mode == "offline" {
					exporter.failure = errors.New("remote unavailable")
				}
				messageRoot := filepath.Join(directory, "messages")
				var reply inbox.Message
				if operation == "resolve" {
					generation := "gen-1"
					if mode == "stale reply" {
						generation = "another-generation"
					}
					reply = sendBoundReply(t, service.root, messageRoot, generation)
					if mode == "disposition conflict" {
						messages, err := inbox.New(messageRoot)
						if err != nil {
							t.Fatal(err)
						}
						if _, _, err := messages.BeginDisposition(service.primaryRecipient, reply.ID, "another-resolution"); err != nil {
							t.Fatal(err)
						}
					}
				}
				accept := func() error {
					if operation == "finish" {
						_, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Close, date)
						return err
					}
					_, err := service.Resolve(
						ctx,
						messageRoot,
						reply.ID.String(),
						taskstate.Close,
						date,
					)
					return err
				}
				err = accept()
				current, readErr := service.store.Read(ctx, "task-1")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if mode != "complete" {
					if err == nil || current.Snapshot().Completion != nil ||
						current.Snapshot().Worker.RemoteResult != (taskstate.RemoteResult{}) {
						t.Fatalf("failed acceptance changed state: %+v %v", current.Snapshot(), err)
					}
					if mode != "offline" && exporter.calls != 0 {
						t.Fatal("invalid acceptance reached remote export")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				worker := current.Snapshot().Worker
				if current.State() != taskstate.Done {
					t.Fatal("prepared a result without accepting the queue transition")
				}
				result := worker.RemoteResult
				if result.Phase != taskstate.RemoteResultReady || result.Commit != exporter.commit {
					t.Fatal("accepted a result without its selected commit")
				}
				if result.Lease.State != taskstate.LeaseHeld ||
					worker.Lease != before.Snapshot().Worker.Lease {
					t.Fatal("accepted code without a selected commit and held primary lease")
				}
				if err := accept(); err != nil || exporter.calls != 1 {
					t.Fatalf(
						"accepted retry repeated preparation: calls=%d err=%v",
						exporter.calls,
						err,
					)
				}
			})
		}
	}
}
