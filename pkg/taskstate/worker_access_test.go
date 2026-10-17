package taskstate

import (
	"bytes"
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

func remoteAccessTask(t *testing.T) Task {
	t.Helper()
	snapshot := taskForTransition(t).Snapshot()
	worker := workerForTransition()
	worker.Machine, worker.Workspace, worker.Tab = "baker", "workspace-1", "tab-1"
	snapshot.State, snapshot.Worker = InFlight, &worker
	task, err := NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestWorkerAccessSurvivesReloadWithoutSecret(t *testing.T) {
	task := remoteAccessTask(t)
	grant, secret, err := workeraccess.Issue(task.ID(), "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.InstallWorkerAccess(grant)
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil || bytes.Contains(data, secret.Bytes()) {
		t.Fatalf("secret was persisted: %v", err)
	}
	restored, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	access := restored.Snapshot().Worker.Access
	if err := access.Authorize(task.ID(), "gen-1", workeraccess.Receive, secret.Bytes()); err != nil {
		t.Fatalf("authorization after restart: %v", err)
	}
	access.Operations[0] = "land"
	access.Revoked = true
	grant.Operations[0] = "land"
	if err := restored.Snapshot().Worker.Access.Authorize(task.ID(), "gen-1", workeraccess.ListPending, secret.Bytes()); err != nil {
		t.Fatalf("external mutation changed stored grant: %v", err)
	}
	other, _, _ := workeraccess.Issue(task.ID(), "gen-1", workeraccess.Operations())
	if _, err := task.InstallWorkerAccess(other); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("same-generation secret silently replaced: %v", err)
	}
	revoked, err := task.RevokeWorkerAccess("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := revoked.Snapshot().Worker.Access.Authorize(task.ID(), "gen-1", workeraccess.Receive, secret.Bytes()); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) {
		t.Fatalf("revoked grant accepted: %v", err)
	}
}

func TestWorkerAccessRejectsOtherBindingsAndRevokesBeforeRestart(t *testing.T) {
	task := remoteAccessTask(t)
	for _, binding := range []struct{ task, generation string }{
		{"other-task", "gen-1"}, {task.ID(), "gen-other"},
	} {
		grant, _, _ := workeraccess.Issue(
			binding.task,
			binding.generation,
			workeraccess.Operations(),
		)
		if _, err := task.InstallWorkerAccess(grant); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("wrong binding accepted: %v", err)
		}
	}
	grant, _, _ := workeraccess.Issue(task.ID(), "gen-1", workeraccess.Operations())
	task, err := task.InstallWorkerAccess(grant)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := task.ReserveLaunch("gen-2")
	if err != nil {
		t.Fatal(err)
	}
	planned, err := reserved.RecordRestart(RestartPlan{From: "gen-1", To: "gen-2"})
	if err != nil || !planned.Snapshot().Worker.Access.Revoked {
		t.Fatalf("restart retained active old grant: %v", err)
	}
	advanced, err := planned.AdvanceGeneration("gen-2")
	if err != nil || advanced.Snapshot().Worker.Access != nil {
		t.Fatalf("new generation retained old grant: %v", err)
	}
	if task.Snapshot().Worker.Access.Revoked {
		t.Fatal("restart mutated the previous task snapshot")
	}
	local := task.Snapshot()
	local.Worker.Machine = ""
	if _, err := NewMigrated(local); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("local worker accepted a remote grant: %v", err)
	}
}

func TestResultAcceptanceRevokesWorkerAccessBeforeCleanup(t *testing.T) {
	task := remoteAccessTask(t)
	grant, secret, err := workeraccess.Issue(task.ID(), "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.InstallWorkerAccess(grant)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := task.AcceptResult("gen-1", Close, "2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	if err := accepted.Snapshot().Worker.Access.Authorize(task.ID(), "gen-1", workeraccess.SendReply, secret.Bytes()); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) {
		t.Fatalf("accepted worker retained reply access: %v", err)
	}
	if task.Snapshot().Worker.Access.Revoked {
		t.Fatal("acceptance mutated the previous snapshot")
	}
}
