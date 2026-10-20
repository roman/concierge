package herdr

import (
	"errors"
	"testing"
)

func TestCheckAbsentWorkerInExistingWorkspace(t *testing.T) {
	for _, kind := range []string{"absent", "tab", "pane", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			tabs := `{"result":{"tabs":[{"workspace_id":"w1","tab_id":"w1:t2"}]}}`
			panes := `{"result":{"panes":[{"workspace_id":"w1","pane_id":"w1:p2"}]}}`
			if kind == "tab" {
				tabs = `{"result":{"tabs":[{"workspace_id":"w1","tab_id":"w1:t1"}]}}`
			}
			if kind == "pane" {
				panes = `{"result":{"panes":[{"workspace_id":"w1","pane_id":"w1:p1"}]}}`
			}
			if kind == "unknown" {
				panes = `{}`
			}
			runner := &fakeRunner{responses: []runResponse{
				{stderr: []byte(`{"error":{"code":"agent_not_found"}}`), err: errors.New("exit 1")},
				{stdout: []byte(`{"result":{"workspaces":[{"workspace_id":"w1"}]}}`)},
				{stdout: []byte(tabs)}, {stdout: []byte(panes)},
			}}
			err := (Client{Runner: runner}).CheckLaunchAbsent(
				t.Context(),
				LaunchResources{Name: "worker", Tab: "w1:t1", Pane: "w1:p1"},
			)
			if (err != nil) != (kind != "absent") {
				t.Fatalf("absence: %v", err)
			}
		})
	}
}
