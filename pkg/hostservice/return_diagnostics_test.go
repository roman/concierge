package hostservice

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestReturnDiagnosticsReachHostLogAndResponse(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	server := fixture.server
	server.preparationMachines = launchLocationCatalog{fixture.profile}
	runner := &retirementLeaseSSH{
		t:       t,
		request: fixture.request.Worktree,
		lease:   fixture.request.Lease,
		outcome: remoteworker.WorktreeUnchanged,
	}
	server.preparationSSH = runner
	var diagnostics bytes.Buffer
	server.config.Diagnostics = &diagnostics
	if _, err := server.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		task, err := task.CompleteInitialLaunch("gen-1")
		if err != nil {
			return taskstate.Task{}, err
		}
		task, err = task.AcceptResult("gen-1", taskstate.Close, "2026-10-23")
		if err != nil {
			return taskstate.Task{}, err
		}
		return task.RetireResult("gen-1")
	}); err != nil {
		t.Fatal(err)
	}
	runner.removed = []string{fixture.request.Lease.Path + "/generated"}
	response, err := server.performOperation(
		t.Context(),
		Request{
			Operation:  ReleaseWorktree,
			Transition: &TransitionRequest{TaskID: "task-1", Generation: "gen-1"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.RemovedPaths) != 1 || response.RemovedPaths[0] != runner.removed[0] {
		t.Fatalf("response: %+v", response)
	}
	encoded, _ := json.Marshal(runner.removed[0])
	if !strings.Contains(diagnostics.String(), "return_removed_path: "+string(encoded)) {
		t.Fatalf("host log: %s", &diagnostics)
	}
}
