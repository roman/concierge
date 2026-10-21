package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestReportCommandReadsHistoricalEvidence(t *testing.T) {
	for _, kind := range []string{"scout", "task"} {
		t.Run(kind, func(t *testing.T) {
			root := shortTaskRoot(t)
			store, _ := taskstate.NewStore(root)
			task, err := taskstate.NewQueued(
				taskstate.Definition{ID: "scout-1", Title: "Investigate", Repo: "repo", Kind: kind},
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
			output.Reset()
			bodyArgs := []string{
				"--root",
				root,
				"task",
				"report",
				"scout-1",
				message.ID.String(),
				"--format",
				"body",
			}
			code = Run(context.Background(), bodyArgs, nil, &output, io.Discard)
			if code != 0 || output.String() != "Findings\nwith evidence" {
				t.Fatalf("body report code %d: %q", code, output.String())
			}
			if code := Run(context.Background(), bodyArgs, nil, failedSettingsWriter{}, io.Discard); code != 1 {
				t.Fatalf("failed body output code %d", code)
			}
			output.Reset()
			code = Run(context.Background(), []string{"--root", root, "task", "report", "scout-1"},
				nil, &output, io.Discard)
			want = "count: 1\nreports[1]{reply_id,created_at}:\n  " + quote(
				message.ID.String(),
			) + "," +
				quote(
					message.CreatedAt.Format(time.RFC3339Nano),
				) + "\n"
			if code != 0 || output.String() != want {
				t.Fatalf("list code %d: %s", code, &output)
			}
		})
	}
}

func TestTaskReportRejectsBodyFormatWithoutReply(t *testing.T) {
	for _, format := range []string{"body", "concise", "invalid"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			code := Run(context.Background(), []string{
				"--root", t.TempDir(), "task", "report", "task-1", "--format", format,
			}, nil, &output, io.Discard)
			if code != 2 || !strings.Contains(output.String(), "error:") {
				t.Fatalf("code %d: %s", code, &output)
			}
			if format != "invalid" && !strings.Contains(output.String(), "reply ID") {
				t.Fatalf("missing reply ID guidance: %s", &output)
			}
		})
	}
}

func TestTaskReportListEmptyAndUnknownTask(t *testing.T) {
	root := shortTaskRoot(t)
	store, _ := taskstate.NewStore(root)
	task, err := taskstate.NewQueued(
		taskstate.Definition{ID: "task-1", Title: "Build", Repo: "repo", Kind: "task"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		taskID string
		code   int
		text   string
	}{
		{"task-1", 0, "count: 0\nreports[0]{reply_id,created_at}:\n"},
		{"missing", 1, ""},
	} {
		var output bytes.Buffer
		code := Run(context.Background(), []string{"--root", root, "task", "report", test.taskID},
			nil, &output, io.Discard)
		if code != test.code || (test.code == 0 && output.String() != test.text) {
			t.Fatalf("%s: code %d, %s", test.taskID, code, &output)
		}
	}
}
