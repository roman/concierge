package herdr

import (
	"context"
	"fmt"
	"testing"
)

func TestWorkspaceDisplayLabels(t *testing.T) {
	for _, label := range []string{"tincan/cleanup", "concierge-gen-1"} {
		t.Run(label, func(t *testing.T) {
			pane := runResponse{stdout: []byte(launchPaneJSON)}
			creation := runResponse{
				stdout: []byte(
					fmt.Sprintf(
						`{"result":{"workspace":{"workspace_id":"w1","label":%q},"tab":{"workspace_id":"w1","tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}}`,
						label,
					),
				),
			}
			runner := &fakeRunner{responses: []runResponse{creation, pane}}
			if _, err := (Client{Runner: runner}).CreateWorkspace(context.Background(), WorkspaceInput{Directory: "/worker", Label: label}); err != nil {
				t.Fatal(err)
			}
			runner = &fakeRunner{responses: []runResponse{
				{
					stdout: []byte(
						fmt.Sprintf(
							`{"result":{"workspaces":[{"workspace_id":"w1","label":%q}]}}`,
							label,
						),
					),
				},
				{
					stdout: []byte(
						`{"result":{"panes":[{"workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2"}]}}`,
					),
				},
				pane,
			}}
			if _, err := (Client{Runner: runner}).FindWorkspace(context.Background(), label); err != nil {
				t.Fatal(err)
			}
		})
	}
}
