package taskview

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestWorkerShowsPendingAndHeldPrimaryResultResources(t *testing.T) {
	worker := viewWorker()
	location := taskstate.RemoteLease{
		Machine:        "machine-1",
		Target:         "synthetic-machine",
		PrimaryRoot:    "/primary/state",
		PrimaryProject: "/primary/repo",
		Base:           "/r",
		Repository:     "repo",
		ClientCommit:   strings.Repeat("b", 40),
		Backend:        taskstate.RemoteGit,
	}
	worker.Machine, worker.Project, worker.Worktree = location.Machine, location.ProjectPath(), location.PoolRoot()+"/slot/repo"
	worker.Lease = taskstate.WorktreeLease{
		Remote:     location,
		ID:         "native-lease",
		Holder:     "native-holder",
		Project:    worker.Project,
		Path:       worker.Worktree,
		Branch:     "task/result",
		BaseCommit: strings.Repeat("a", 40),
		State:      taskstate.LeaseHeld,
	}
	task := viewTask(t, "task-1", taskstate.InFlight, worker, nil)
	commit := strings.Repeat("c", 40)
	var err error
	task, err = task.SelectRemoteResult("gen-1", commit, "primary-holder")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"selected", "held"} {
		if phase == "held" {
			task, err = task.RequestRemoteResultLease("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			task, err = task.RecordRemoteResultLease(
				"gen-1",
				taskstate.WorktreeLease{
					ID:         "primary-lease",
					Holder:     "primary-holder",
					Project:    "/primary/repo",
					Path:       "/primary/pool/slot/repo",
					Branch:     "task/result",
					BaseCommit: commit,
					State:      taskstate.LeaseHeld,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		var output bytes.Buffer
		if err := WriteWorker(&output, task); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"remote_result:\n", "  commit: \"" + commit + "\"\n", "  primary_holder: \"primary-holder\"\n"} {
			if !strings.Contains(output.String(), expected) {
				t.Fatalf("missing result recovery field %q: %s", expected, &output)
			}
		}
		if phase == "selected" && !strings.Contains(output.String(), "  primary_lease: null\n") {
			t.Fatal("pending selection invented a primary lease")
		}
		if phase == "held" &&
			!strings.Contains(
				output.String(),
				"  primary_lease:\n    id: \"primary-lease\"\n    state: held\n",
			) {
			t.Fatal("held primary lease is not inspectable")
		}
	}
}
