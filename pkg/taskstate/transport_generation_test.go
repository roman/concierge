package taskstate

import (
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

func TestRemoteTransportConfirmationRequiresCurrentActiveGrantAndSurvivesReload(t *testing.T) {
	task := remoteAccessTask(t)
	if _, err := task.ConfirmTransportGeneration("gen-1"); err == nil {
		t.Fatal("confirmed transport without an active grant")
	}
	grant, _, err := workeraccess.Issue(task.ID(), "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.InstallWorkerAccess(grant)
	if err == nil {
		task, err = task.ConfirmTransportGeneration("gen-1")
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Decode(data)
	if err != nil || reloaded.Snapshot().Worker.TransportGeneration != "gen-1" {
		t.Fatalf("lost transport confirmation: %v", err)
	}
	for _, mode := range []string{"different generation", "local worker", "missing grant"} {
		snapshot := task.Snapshot()
		switch mode {
		case "different generation":
			snapshot.Worker.TransportGeneration = "other-generation"
		case "local worker":
			snapshot.Worker.Machine = ""
		case "missing grant":
			snapshot.Worker.Access = nil
		}
		if _, err := NewMigrated(snapshot); err == nil {
			t.Fatalf("accepted %s transport confirmation", mode)
		}
	}
	legacy := task.Snapshot()
	legacy.Worker.PendingGeneration = "legacy-next"
	legacyTask, err := NewMigrated(legacy)
	if err == nil {
		legacyTask, err = legacyTask.AdvanceGeneration("legacy-next")
	}
	if err != nil || legacyTask.Snapshot().Worker.TransportGeneration != "" ||
		legacyTask.Snapshot().Worker.Access != nil {
		t.Fatalf("legacy generation advancement retained transport authority: %v", err)
	}
	task, err = task.ReserveLaunch("gen-2")
	if err == nil {
		task, err = task.RecordRestart(RestartPlan{From: "gen-1", To: "gen-2"})
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.ConfirmTransportGeneration("gen-1"); err == nil {
		t.Fatal("confirmed a revoked generation")
	}
	task, err = task.AdvanceGeneration("gen-2")
	if err != nil || task.Snapshot().Worker.TransportGeneration != "" ||
		task.Snapshot().Worker.Access != nil {
		t.Fatalf("new generation retained old transport authority: %v", err)
	}
	task, err = task.CompleteLaunch()
	if err != nil {
		t.Fatal(err)
	}
	grant, _, err = workeraccess.Issue(task.ID(), "gen-2", workeraccess.Operations())
	if err == nil {
		task, err = task.InstallWorkerAccess(grant)
	}
	if err == nil {
		task, err = task.ConfirmTransportGeneration("gen-2")
	}
	if err != nil || task.Snapshot().Worker.TransportGeneration != "gen-2" {
		t.Fatalf("new generation could not confirm its own transport: %v", err)
	}
}
