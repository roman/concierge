package taskops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerSessionListsMetadataAndSeparatesBootstrapFromWork(t *testing.T) {
	service, messages, assignment, secret := workerSessionFixture(t)
	ctx := context.Background()
	session, err := service.WorkerSession(messages.Root(), "task-1", "gen-existing", secret.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := session.Pending(ctx)
	data, _ := json.Marshal(pending)
	if err != nil || len(pending) != 1 || pending[0].ID != assignment.ID.String() ||
		strings.Contains(string(data), "Complete the assigned work") {
		t.Fatalf("pending list leaked work or omitted identity: %s %v", data, err)
	}
	if _, err := session.ReadBootstrap(ctx, assignment.ID.String()); err == nil {
		t.Fatal("bootstrap inspection accepted an assignment")
	}
	if err := session.AcknowledgeBootstrap(ctx, assignment.ID.String()); err == nil {
		t.Fatal("bootstrap acknowledgement accepted an assignment")
	}
	if _, err := messages.Read(assignment.Recipient, assignment.ID); err != nil {
		t.Fatalf("failed bootstrap operation changed an assignment: %v", err)
	}
	for _, trial := range []struct{ name, state, task, generation string }{
		{"current", filepath.Join(service.root, "tasks"), "task-1", "gen-existing"},
		{"other root", "/other/tasks", "task-1", "gen-existing"},
		{"other task", filepath.Join(service.root, "tasks"), "task-2", "gen-existing"},
		{"other generation", filepath.Join(service.root, "tasks"), "task-1", "gen-old"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			body, _ := json.Marshal(taskBootstrap{Kind: "task-bootstrap", State: trial.state,
				Task: trial.task, SpawnGeneration: trial.generation})
			message, err := messages.Send(assignment.Recipient, trial.name, string(body))
			if err != nil {
				t.Fatal(err)
			}
			bootstrap, err := session.ReadBootstrap(ctx, message.ID.String())
			if trial.name != "current" {
				if err == nil || session.AcknowledgeBootstrap(ctx, message.ID.String()) == nil {
					t.Fatal("mismatched bootstrap was accepted")
				}
				if _, err := messages.Read(message.Recipient, message.ID); err != nil {
					t.Fatalf("mismatched bootstrap changed: %v", err)
				}
				return
			}
			if err != nil || bootstrap.Task != "task-1" || bootstrap.Generation != "gen-existing" {
				t.Fatalf("current bootstrap: %+v %v", bootstrap, err)
			}
			for range 2 {
				if err := session.AcknowledgeBootstrap(ctx, message.ID.String()); err != nil {
					t.Fatalf("idempotent bootstrap acknowledgement: %v", err)
				}
			}
		})
	}
}

func TestWorkerSessionSettingsUseOnlyTheAssignedRepository(t *testing.T) {
	service, messages, _, secret := workerSessionFixture(t)
	session, err := service.WorkerSession(messages.Root(), "task-1", "gen-existing", secret.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	read := func(repo string) (WorkerSettings, error) {
		reads++
		if repo != "exmachina" {
			t.Fatalf("read another repository: %s", repo)
		}
		return WorkerSettings{Merge: "on", Delivery: "fast-forward"}, nil
	}
	settings, err := session.Settings(context.Background(), read)
	if err != nil || settings.Repo != "exmachina" || settings.Merge != "on" ||
		settings.Delivery != "fast-forward" {
		t.Fatalf("task settings: %+v %v", settings, err)
	}
	if _, err := service.store.Update(context.Background(), "task-1", func(current taskstate.Task) (taskstate.Task, error) {
		return current.RevokeWorkerAccess("gen-existing")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Settings(context.Background(), read); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) ||
		reads != 1 {
		t.Fatalf("revoked session read settings: count=%d error=%v", reads, err)
	}
}
