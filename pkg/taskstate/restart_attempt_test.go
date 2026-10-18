package taskstate

import "testing"

func TestRemoteRestartAttemptSurvivesReloadUntilNativeConfirmation(t *testing.T) {
	snapshot := remoteAccessTask(t).Snapshot()
	lease := remoteLeaseFixture()
	snapshot.Worker.Lease = lease
	snapshot.Worker.Machine, snapshot.Worker.Project, snapshot.Worker.Worktree = lease.Remote.Machine, lease.Project, lease.Path
	task, err := NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.RequestRestartAgent("gen-1"); err == nil {
		t.Fatal("recorded a native attempt without a restart reservation")
	}
	task, err = task.ReserveLaunch("gen-2")
	if err == nil {
		task, err = task.RecordRestart(RestartPlan{From: "gen-1", To: "gen-2"})
	}
	if err == nil {
		task, err = task.AdvanceGeneration("gen-2")
	}
	if err == nil {
		task, err = task.RequestRestartAgent("gen-2")
	}
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := task.AdvanceGeneration("gen-2"); err != nil ||
		repeated.Snapshot().Worker.LaunchAttemptGeneration != "gen-2" {
		t.Fatalf("generation retry erased an unknown native attempt: %v", err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(data)
	if err != nil || restored.Snapshot().Worker.LaunchAttemptGeneration != "gen-2" {
		t.Fatalf("lost the possibly submitted native attempt: %v", err)
	}
	for _, invalid := range []string{"generation", "reservation", "plan", "local"} {
		snapshot := task.Snapshot()
		switch invalid {
		case "generation":
			snapshot.Worker.LaunchAttemptGeneration = "different"
		case "reservation":
			snapshot.Worker.PendingLaunch = ""
		case "plan":
			snapshot.Worker.Restarts = nil
		case "local":
			snapshot.Worker.Machine, snapshot.Worker.Lease = "", WorktreeLease{}
		}
		if _, err := NewMigrated(snapshot); err == nil {
			t.Fatalf("accepted inconsistent %s attempt", invalid)
		}
	}
	completed, err := restored.CompleteLaunch()
	if err != nil || completed.Snapshot().Worker.LaunchAttemptGeneration != "" ||
		completed.Snapshot().Worker.TransportGeneration != "" {
		t.Fatalf("native confirmation did not leave transport setup pending: %v", err)
	}
}
