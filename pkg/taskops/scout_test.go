package taskops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func makeScout(t *testing.T, service *Service) {
	t.Helper()
	if _, err := service.store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Definition.Kind = "scout"
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScoutAssignmentContractRequiresCurrentGeneration(t *testing.T) {
	for _, generation := range []string{"gen-1", "gen-old"} {
		t.Run(generation, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			makeScout(t, service)
			result, err := service.ReadAssignment(
				context.Background(),
				messages.Root(),
				"task-1",
				assignment.ID.String(),
				generation,
			)
			if err != nil {
				t.Fatal(err)
			}
			if generation != "gen-1" {
				if result.State != AssignmentStale || result.Body != "" {
					t.Fatalf("stale scout: %#v", result)
				}
				return
			}
			for _, part := range []string{"Build the feature", "read-only scout", "Never edit", "commit", "push", "report", "task reply send"} {
				if !strings.Contains(result.Body, part) {
					t.Fatalf("missing %q: %s", part, result.Body)
				}
			}
		})
	}
}

func TestReportSurvivesCleanup(t *testing.T) {
	for _, kind := range []string{"scout", "task"} {
		t.Run(kind, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			if kind == "scout" {
				makeScout(t, service)
			}
			input := replyInput(assignment)
			ctx := context.Background()
			result, err := service.SendReply(ctx, messages.Root(), input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Resolve(ctx, messages.Root(), result.Message.ID.String(), taskstate.Close, "2026-10-14"); err != nil {
				t.Fatal(err)
			}
			stop := &stopRunner{
				socket: filepath.Join(t.TempDir(), "herdr.sock"), exists: true,
				activeTab: "viewer-tab", viewer: "cleared", workspace: "workspace-1", tab: "tab-1",
			}
			if err := service.Stop(ctx, "task-1", "gen-1", herdr.Client{SessionRunner: stop}); err != nil {
				t.Fatal(err)
			}
			if stop.closes != 1 {
				t.Fatalf("pane closes: %d", stop.closes)
			}
			if _, err := service.Retire(ctx, "task-1", "gen-1", herdr.Client{SessionRunner: &retirementRunner{response: `{"error":{"code":"pane_not_found"}}`}}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(messages.Root()); err != nil {
				t.Fatal(err)
			}
			report, err := service.ReadReport(ctx, "task-1", result.Message.ID.String())
			if err != nil || report.Body != input.Body || report.SpawnGeneration != "gen-1" {
				t.Fatalf("archived report: %#v, %v", report, err)
			}
			reports, err := service.ListReports(ctx, "task-1")
			if err != nil || len(reports) != 1 || reports[0].ID != result.Message.ID {
				t.Fatalf("archived list: %#v, %v", reports, err)
			}
		})
	}
}

func TestArchiveFailureLeavesAssignmentPendingAndRetryRepairs(t *testing.T) {
	for _, kind := range []string{"scout", "task"} {
		t.Run(kind, func(t *testing.T) {
			service, messages, assignment := assignmentFixture(t)
			if kind == "scout" {
				makeScout(t, service)
			}
			path := filepath.Join(service.root, "reports")
			if err := os.WriteFile(path, []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			input := replyInput(assignment)
			first, err := service.SendReply(context.Background(), messages.Root(), input)
			if err == nil || first.Message.ID.String() == "" ||
				first.Assignment != ReplyAcknowledgementUnconfirmed {
				t.Fatalf("archive failure: %#v, %v", first, err)
			}
			if _, err := messages.Read(assignment.Recipient, assignment.ID); err != nil {
				t.Fatalf("lost assignment: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			second, err := service.SendReply(context.Background(), messages.Root(), input)
			if err != nil || second.Message.ID != first.Message.ID {
				t.Fatalf("retry: %#v, %v", second, err)
			}
			report, err := service.ReadScoutReport(
				context.Background(),
				"task-1",
				second.Message.ID.String(),
			)
			if err != nil || report.Body != input.Body {
				t.Fatalf("report: %#v, %v", report, err)
			}
		})
	}
}
