package conciergecli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestHeartbeatQuietLimit(t *testing.T) {
	for _, limit := range []int{5, 2, 0} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			if limit != 5 {
				invoke(t, root, "", "heartbeat", "set", "--quiet-limit", fmt.Sprint(limit))
			}
			now := time.Now()
			check := func(beat int, wake, text string) string {
				t.Helper()
				var out bytes.Buffer
				err := claimHeartbeat(context.Background(), root, time.Minute,
					now.Add(time.Duration(beat)*time.Minute), wake,
					heartbeatStatus{Active: true, Text: text}, &out)
				if err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			check(0, "checkpoint", "worker: idle\n")
			for beat := 1; beat <= 9; beat++ {
				got := check(beat, "checkpoint", "worker: idle\n")
				want := "status: due"
				if limit > 0 && beat > limit+1 {
					want = "status: quiet"
				}
				if !strings.Contains(got, want) {
					t.Fatalf("beat %d: %s", beat, got)
				}
				if limit > 0 && beat == limit+1 &&
					!strings.Contains(got, "pausing until something changes") {
					t.Fatal(got)
				}
			}
			if got := check(10, "inbox", ""); !strings.Contains(got, "status: quiet") {
				t.Fatal(got)
			}
			if got := check(11, "checkpoint", "worker: idle\n"); limit > 0 &&
				!strings.Contains(got, "paused: yes") {
				t.Fatal(got)
			}
			if got := check(12, "checkpoint", "worker: running\n"); !strings.Contains(
				got,
				"status: due",
			) {
				t.Fatal(got)
			}
		})
	}
}

func TestHeartbeatResumesOnStatusChanges(t *testing.T) {
	for _, change := range []string{"worker state", "reply", "cleanup", "action added", "action resolved", "action reasked"} {
		t.Run(change, func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			messageRoot := t.TempDir()
			executable, _ := controlFixture(t)
			worker := &taskstate.Worker{
				Name: "worker", Harness: "codex", Session: "work", Pane: "pane-1",
				Workspace: "workspace-1", Tab: "tab:1", Project: "/repo", Worktree: "/worktree",
				Generation: "gen-1", AgentArgs: []string{"resume", "thread-1"},
			}
			seedViewTask(t, root, "worker", taskstate.InFlight, worker, nil)
			if change == "action resolved" || change == "action reasked" {
				invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
			}
			now := time.Now()
			check := func(beat int) string {
				t.Helper()
				status, err := collectHeartbeatStatus(
					context.Background(),
					root,
					messageRoot,
					"concierge",
					executable,
				)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if err := claimHeartbeat(context.Background(), root, time.Minute,
					now.Add(time.Duration(beat)*time.Minute), "checkpoint", status, &out); err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			for beat := 0; beat <= 6; beat++ {
				check(beat)
			}
			if got := check(7); !strings.Contains(got, "paused: yes") {
				t.Fatal(got)
			}
			switch change {
			case "worker state":
				t.Setenv("TEST_CONTROL_BUSY", "1")
			case "reply":
				messages, _ := inbox.New(messageRoot)
				recipient, _ := inbox.ParseRecipient("concierge")
				if _, err := messages.Send(recipient, "task-reply:"+root+":worker:gen-1:assignment", "Result"); err != nil {
					t.Fatal(err)
				}
				seedViewTask(t, root, "worker", taskstate.Queued, nil, nil)
			case "cleanup":
				seedViewTask(t, root, "worker", taskstate.InFlight, nil, &taskstate.Completion{
					Generation: "gen-1", Mode: taskstate.Retain, Phase: taskstate.Retired,
				})
			case "action added", "action reasked":
				invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
			case "action resolved":
				invoke(t, root, "", "operator", "resolve", "approval")
			}
			if got := check(8); !strings.Contains(got, "status: due") ||
				strings.Contains(got, "pause:") {
				t.Fatal(got)
			}
			if got := check(9); !strings.Contains(got, "status: due") {
				t.Fatal(got)
			}
		})
	}
}

func TestHeartbeatQuietSettings(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	path := filepath.Join(root, "config", "heartbeat.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"interval":"2m"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := invoke(t, root, "", "heartbeat", "show"); !strings.Contains(got, "quiet_limit: 5") {
		t.Fatal(got)
	}
	invoke(t, root, "", "heartbeat", "set", "--quiet-limit", "0")
	invoke(t, root, "", "heartbeat", "set", "--interval", "3m")
	if got := invoke(t, root, "", "heartbeat", "show"); !strings.Contains(got, "quiet_limit: 0") ||
		!strings.Contains(got, "interval: 3m0s") {
		t.Fatal(got)
	}
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"--root", root, "heartbeat", "set", "--quiet-limit", "-1"}, strings.NewReader(""), &out, &out); code != 2 {
		t.Fatal(code, out.String())
	}
}

func TestOperatorReminderQuietLimit(t *testing.T) {
	for _, limit := range []int{5, 2, 0} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			if limit != 5 {
				invoke(t, root, "", "heartbeat", "set", "--quiet-limit", fmt.Sprint(limit))
			}
			invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
			now := time.Now()
			check := func(beat int) string {
				t.Helper()
				var out bytes.Buffer
				if code := runOperatorReminder(context.Background(), root, time.Minute,
					now.Add(time.Duration(beat)*time.Minute), &out); code != 0 {
					t.Fatal(out.String())
				}
				return out.String()
			}
			for beat := 0; beat < 8; beat++ {
				want := "status: due"
				if limit > 0 && beat >= limit {
					want = "status: quiet"
				}
				if got := check(beat); !strings.Contains(got, want) {
					t.Fatalf("reminder %d: %s", beat+1, got)
				}
			}
			invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
			if got := check(9); !strings.Contains(got, "status: due") {
				t.Fatal(got)
			}
			invoke(t, root, "", "operator", "resolve", "approval")
			invoke(t, root, "", "operator", "ask", "replacement", "Approve the replacement")
			if got := check(10); !strings.Contains(got, "Approve the replacement") {
				t.Fatal(got)
			}
		})
	}
}

func TestOperatorReminderResumesOnItemChanges(t *testing.T) {
	for _, change := range []string{"action added", "action resolved", "completion phase", "cleanup finished"} {
		t.Run(change, func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
			worker := &taskstate.Worker{
				Name: "worker", Harness: "codex", Session: "work", Pane: "pane-1",
				Workspace: "workspace-1", Tab: "tab:1", Project: "/repo", Worktree: "/worktree",
				Generation: "gen-1", AgentArgs: []string{"resume", "thread-1"},
			}
			completion := &taskstate.Completion{
				Generation: "gen-1", Mode: taskstate.Retain, Phase: taskstate.Accepted,
			}
			seedViewTask(t, root, "worker", taskstate.InFlight, worker, completion)
			now := time.Now()
			check := func(beat int) string {
				t.Helper()
				var out bytes.Buffer
				if code := runOperatorReminder(context.Background(), root, time.Minute,
					now.Add(time.Duration(beat)*time.Minute), &out); code != 0 {
					t.Fatal(out.String())
				}
				return out.String()
			}
			for beat := 0; beat < 5; beat++ {
				if got := check(beat); !strings.Contains(got, "status: due") {
					t.Fatal(got)
				}
			}
			if got := check(5); !strings.Contains(got, "status: quiet") {
				t.Fatal(got)
			}
			switch change {
			case "action added":
				invoke(t, root, "", "operator", "ask", "second", "Second request")
			case "action resolved":
				invoke(t, root, "", "operator", "resolve", "approval")
			case "completion phase":
				completion.Phase = taskstate.Retired
				seedViewTask(t, root, "worker", taskstate.InFlight, nil, completion)
			case "cleanup finished":
				seedViewTask(t, root, "worker", taskstate.Queued, nil, nil)
			}
			if got := check(6); !strings.Contains(got, "status: due") ||
				strings.Contains(got, "pause:") {
				t.Fatal(got)
			}
		})
	}
}
