package taskstate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func remoteLeaseFixture() WorktreeLease {
	return WorktreeLease{
		ID:         "remote-lease",
		Holder:     "task-initial-generation",
		Project:    "/remote/base/projects/repo",
		Path:       "/remote/base/worktrees/slot/repo",
		Branch:     "feat/task",
		BaseCommit: strings.Repeat("a", 40),
		State:      LeaseHeld,
		Remote: RemoteLease{
			Machine:        "machine-1",
			Target:         "baker",
			PrimaryRoot:    "/primary/state",
			PrimaryProject: "/primary/repo",
			Base:           "/remote/base",
			Repository:     "repo",
			ClientCommit:   strings.Repeat("b", 40),
			Backend:        RemoteTreehouse,
		},
	}
}

func TestRemoteLeaseSurvivesWorkerRetirementAndReturn(t *testing.T) {
	lease := remoteLeaseFixture()
	lease.Remote.Authentication = sshcommand.ForwardedAgent
	task, err := NewMigrated(
		Snapshot{
			Definition: Definition{ID: "task-1", Title: "Remote task", Repo: "repo", Kind: "task"},
			State:      InFlight,
			Worker: &Worker{
				Name:       "worker",
				Machine:    lease.Remote.Machine,
				Harness:    "codex",
				Session:    "work",
				Workspace:  "workspace-1",
				Tab:        "tab-1",
				Pane:       "pane-1",
				Project:    lease.Project,
				Worktree:   lease.Path,
				Generation: "gen-1",
				AgentArgs:  []string{"resume", "native-id"},
				Lease:      lease,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.AcceptResult("gen-1", Close, "2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.RetireResult("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	retired, found := task.RetiredLease("gen-1")
	if !found || retired != lease || task.Snapshot().Worker != nil {
		t.Fatal("worker retirement lost remote lease identity")
	}
	for _, phase := range []string{"held", "returned"} {
		if phase == "returned" {
			task, err = task.RecordLeaseReturned("gen-1", lease)
			if err != nil {
				t.Fatal(err)
			}
		}
		data, err := task.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`"format":3`)) {
			t.Fatal("retired remote lease lost its format gate")
		}
		restored, err := Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := restored.RetiredLease("gen-1"); got.Remote != lease.Remote {
			t.Fatal("stored receipt lost remote cleanup routing")
		}
		if _, err := Decode(bytes.Replace(data, []byte(`"format":3`), []byte(`"format":2`), 1)); err == nil {
			t.Fatal("format two accepted remote cleanup metadata")
		}
	}
}

func TestRemoteLeaseRejectsForeignPathsAndMissingBackend(t *testing.T) {
	for _, change := range []func(*WorktreeLease){
		func(lease *WorktreeLease) { lease.Remote.Authentication = "unknown" },
		func(lease *WorktreeLease) { lease.Remote.Backend = "" },
		func(lease *WorktreeLease) { lease.Remote.Backend = "unknown" },
		func(lease *WorktreeLease) { lease.Remote.PrimaryProject = "relative" },
		func(lease *WorktreeLease) { lease.Remote.Target = "-option" },
		func(lease *WorktreeLease) { lease.Remote.Repository = "../other" },
		func(lease *WorktreeLease) { lease.Project = "/other/project" },
		func(lease *WorktreeLease) { lease.Path = "/remote/base/worktrees-other/project" },
	} {
		lease := remoteLeaseFixture()
		change(&lease)
		if validWorktreeLease(lease) {
			t.Fatal("accepted unbound remote lease metadata")
		}
	}
}
