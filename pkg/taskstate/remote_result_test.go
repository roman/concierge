package taskstate

import (
	"strings"
	"testing"
)

func remoteResultTask(t *testing.T) Task {
	t.Helper()
	lease := remoteLeaseFixture()
	task, err := NewMigrated(Snapshot{
		Definition: Definition{ID: "task-1", Title: "Remote result", Repo: "repo", Kind: "task"},
		State:      InFlight,
		Worker: &Worker{Name: "worker", Machine: lease.Remote.Machine, Harness: "codex",
			Session: "work", Workspace: "workspace-1", Tab: "tab-1", Pane: "pane-1",
			Project: lease.Project, Worktree: lease.Path, Generation: "gen-1",
			AgentArgs: []string{"resume", "native-id"}, Lease: lease},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestRemoteResultKeepsSelectedCommitAndPrimaryLeaseAcrossRetirement(t *testing.T) {
	task := remoteResultTask(t)
	commit := strings.Repeat("c", 40)
	var err error
	task, err = task.SelectRemoteResult("gen-1", commit, "primary-result-holder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.AcceptResult("gen-1", Close, "2026-10-17"); err == nil {
		t.Fatal("accepted a result before primary materialization")
	}
	if _, err := task.ReserveLaunch("another-generation"); err == nil {
		t.Fatal("restarted a worker with a selected result")
	}
	if _, err := task.SelectRemoteResult("gen-1", strings.Repeat("d", 40), "primary-result-holder"); err == nil {
		t.Fatal("replaced the selected commit")
	}
	task, err = task.RequestRemoteResultLease("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	lease := WorktreeLease{
		ID:         "local-lease",
		Holder:     "primary-result-holder",
		Project:    "/primary/repo",
		Path:       "/primary/pool/slot/repo",
		Branch:     "feat/task",
		BaseCommit: commit,
		State:      LeaseHeld,
	}
	task, err = task.RecordRemoteResultLease("gen-1", lease)
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.ConfirmRemoteResult("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	ready := task.Snapshot().Worker.RemoteResult
	if ready.Commit != commit || ready.Lease != lease || ready.Phase != RemoteResultReady {
		t.Fatal("lost the selected result or primary lease")
	}
	task, err = task.AcceptResult("gen-1", Close, "2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.RetireResult("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Decode(data)
	if err != nil || loaded.Snapshot().Receipt.RemoteResult != ready {
		t.Fatalf("retirement lost immutable delivery evidence: %v", err)
	}
	invalid := loaded.Snapshot()
	invalid.Receipt.RemoteResult.Phase = RemoteResultLeaseCaptured
	if _, err := NewMigrated(invalid); err == nil {
		t.Fatal("decoded a receipt without confirmed materialization")
	}
}

func TestRemoteResultRejectsRebindingAndOutOfOrderMaterialization(t *testing.T) {
	task := remoteResultTask(t)
	commit := strings.Repeat("c", 40)
	for _, selected := range []string{"HEAD", "", strings.Repeat("C", 40)} {
		if _, err := task.SelectRemoteResult("gen-1", selected, "holder"); err == nil {
			t.Fatal("selected a mutable or malformed result")
		}
	}
	selected, err := task.SelectRemoteResult("gen-1", commit, "holder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selected.ConfirmRemoteResult("gen-1"); err == nil {
		t.Fatal("confirmed a result without a held primary lease")
	}
	requested, err := selected.RequestRemoteResultLease("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	lease := WorktreeLease{ID: "local-lease", Holder: "holder", Project: "/primary/repo",
		Path: "/primary/pool/slot/repo", Branch: "feat/task", BaseCommit: commit, State: LeaseHeld}
	for _, field := range []string{"project", "holder", "branch", "commit", "state", "remote"} {
		changed := lease
		switch field {
		case "project":
			changed.Project = "/other/project"
		case "holder":
			changed.Holder = "other-holder"
		case "branch":
			changed.Branch = "other-branch"
		case "commit":
			changed.BaseCommit = strings.Repeat("d", 40)
		case "state":
			changed.State = LeaseReturned
		case "remote":
			changed = remoteLeaseFixture()
		}
		if _, err := requested.RecordRemoteResultLease("gen-1", changed); err == nil {
			t.Fatalf("captured a primary lease with changed %s", field)
		}
	}
}

func TestRemoteResultRejectsInvalidCompletionAndReturnedActiveLease(t *testing.T) {
	task := remoteResultTask(t)
	selected, err := task.SelectRemoteResult("gen-1", strings.Repeat("c", 40), "holder")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := selected.Snapshot()
	snapshot.State = Done
	snapshot.Definition.Closed = "2026-10-17"
	snapshot.Completion = &Completion{Generation: "gen-1", Mode: Close, Phase: Accepted}
	if _, err := NewMigrated(snapshot); err == nil {
		t.Fatal("decoded acceptance before result materialization")
	}
	snapshot = task.Snapshot()
	snapshot.Worker.RemoteResult = RemoteResult{
		Commit: strings.Repeat("c", 40),
		Holder: "holder",
		Phase:  RemoteResultReady,
		Lease: WorktreeLease{
			ID:         "local-lease",
			Holder:     "holder",
			Project:    "/primary/repo",
			Path:       "/primary/pool/slot/repo",
			Branch:     "feat/task",
			BaseCommit: strings.Repeat("c", 40),
			State:      LeaseReturned,
		},
	}
	if _, err := NewMigrated(snapshot); err == nil {
		t.Fatal("decoded an active worker with a returned result lease")
	}
}
