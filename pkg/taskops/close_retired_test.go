package taskops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
	"github.com/roman/tincan/pkg/inbox"
)

func TestCloseRetiredOrphanRequiresAbsenceAndNoCurrentReply(t *testing.T) {
	for _, mode := range []string{"absent", "present", "uncertain", "generation", "pending reply", "stale reply", "preserved reply"} {
		t.Run(mode, func(t *testing.T) {
			service, _, config, _ := pooledLaunchFixture(t)
			before, err := service.ContinueInitialLaunch(t.Context(), "task-1", "gen-1", config)
			if err != nil {
				t.Fatal(err)
			}
			worker := before.Snapshot().Worker
			messages := t.TempDir()
			if mode == "pending reply" || mode == "stale reply" || mode == "preserved reply" {
				mailbox, err := inbox.New(messages)
				if err != nil {
					t.Fatal(err)
				}
				recipient, _ := inbox.ParseRecipient("task-1")
				envelope, err := json.Marshal(taskprotocol.Assignment{Kind: "task-assignment",
					State: filepath.Join(service.root, "tasks"), Task: "task-1",
					SpawnGeneration: "gen-1", Body: "Complete the work"})
				if err != nil {
					t.Fatal(err)
				}
				assignment, err := mailbox.Send(recipient, "assignment", string(envelope))
				if err != nil {
					t.Fatal(err)
				}
				delivery, err := service.SendReply(t.Context(), messages, ReplyInput{
					TaskID: "task-1", Generation: "gen-1", Key: "result", Body: "Result",
					AssignmentID: assignment.ID.String(),
				})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "stale reply" || mode == "preserved reply" {
					before, err = service.store.Update(
						t.Context(),
						"task-1",
						func(task taskstate.Task) (taskstate.Task, error) {
							snapshot := task.Snapshot()
							snapshot.Worker.Generation = "gen-2"
							if mode == "preserved reply" {
								snapshot.Worker.Restarts = []taskstate.RestartPlan{{
									From: "gen-1", To: "gen-2", Replies: []string{delivery.Message.ID.String()},
								}}
							}
							return taskstate.NewMigrated(snapshot)
						},
					)
					if err != nil {
						t.Fatal(err)
					}
					worker = before.Snapshot().Worker
				}
			}
			script := "#!/bin/sh\nprintf '%s\\n' '{\"error\":{\"code\":\"pane_not_found\"}}'\nexit 1\n"
			if mode == "present" {
				script = "#!/bin/sh\nprintf '%s\\n' '{\"result\":{\"pane\":{\"pane_id\":\"" + worker.Pane + "\"}}}'\n"
			}
			if mode == "uncertain" {
				script = "#!/bin/sh\nexit 72\n"
			}
			executable := filepath.Join(t.TempDir(), "herdr")
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			generation := worker.Generation
			if mode == "generation" {
				generation = "wrong-generation"
			}
			closed, err := service.CloseRetired(t.Context(), messages, "task-1", generation,
				"Superseded", "2026-10-21", herdr.NewClient(executable))
			if mode != "absent" && mode != "stale reply" {
				if err == nil {
					t.Fatal("unsafe closure succeeded")
				}
				after, readErr := service.store.Read(t.Context(), "task-1")
				if readErr != nil || !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
					t.Fatalf("refusal changed binding: %v", readErr)
				}
				return
			}
			if err != nil || closed.State() != taskstate.Done || closed.Snapshot().Worker != nil {
				t.Fatalf("closure failed: %v", err)
			}
			lease, found, err := config.Pool.Find(t.Context(), worker.Project, worker.Lease.Holder)
			if err != nil || !found || lease.ID != worker.Lease.ID {
				t.Fatalf("closure returned the lease: %v", err)
			}
			retried, err := service.CloseRetired(t.Context(), "", "task-1", generation,
				"Superseded", "2026-10-21", herdr.Client{})
			if err != nil || !reflect.DeepEqual(closed.Snapshot(), retried.Snapshot()) {
				t.Fatalf("retry changed closure or inspected external state: %v", err)
			}
			var workers, brief bytes.Buffer
			if err := taskview.WriteWorkers(&workers, []taskstate.Task{closed}); err != nil {
				t.Fatal(err)
			}
			recipient, _ := inbox.ParseRecipient("concierge")
			if err := taskview.WriteBrief(&brief, []taskstate.Task{closed}, nil,
				taskview.BriefOptions{Limit: 10, Recipient: recipient}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(workers.String(), "workers[0]") ||
				strings.Contains(brief.String(), worker.Pane) {
				t.Fatal("closed worker remains in the worker views")
			}
		})
	}
}
