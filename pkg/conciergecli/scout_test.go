package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestScoutReportCommandReadsHistoricalEvidence(t *testing.T) {
	root := shortTaskRoot(t)
	store, _ := taskstate.NewStore(root)
	task, err := taskstate.NewQueued(
		taskstate.Definition{ID: "scout-1", Title: "Investigate", Repo: "repo", Kind: "scout"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	archive, _ := inbox.New(filepath.Join(root, "reports"))
	recipient, _ := inbox.ParseRecipient("scout-1")
	body, _ := json.Marshal(
		taskprotocol.Reply{
			Kind:            "task-reply",
			State:           filepath.Join(root, "tasks"),
			Task:            "scout-1",
			SpawnGeneration: "gen-1",
			Body:            "Findings\nwith evidence",
		},
	)
	message, err := archive.Send(recipient, "report", string(body))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"--root", root, "task", "report", "scout-1", message.ID.String()},
		nil,
		&output,
		io.Discard,
	)
	want := "status: archived\ntask: \"scout-1\"\ngeneration: \"gen-1\"\nbody: \"Findings\\nwith evidence\"\n"
	if code != 0 || output.String() != want {
		t.Fatalf("report code %d: %s", code, &output)
	}
}
