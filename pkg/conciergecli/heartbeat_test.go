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

func TestUnregisteredLaunchStatus(t *testing.T) {
	for _, overdue := range []bool{false, true} {
		t.Run(fmt.Sprint(overdue), func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			requested := time.Now().UTC()
			if overdue {
				requested = requested.Add(-21 * time.Minute)
			}
			task, err := taskstate.NewMigrated(taskstate.Snapshot{
				Definition: taskstate.Definition{
					ID:    "launching",
					Title: "Launch",
					Repo:  "repo",
					Kind:  "task",
				},
				State: taskstate.Queued,
				LaunchIntent: &taskstate.LaunchIntent{
					Generation: "gen-1", Harness: "codex", Name: "worker", Session: "work",
					Project: "/repo", Worktree: "/worktree", Branch: "task/launch",
					BaseCommit: strings.Repeat("a", 40), Label: "launch",
					Phase: taskstate.LaunchAgentRequested, AgentRequestedAt: requested,
					Endpoint: &taskstate.LaunchEndpoint{
						Workspace: "workspace-1",
						Tab:       "tab:1",
						Pane:      "pane-1",
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			data, err := task.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "records", "launching.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			status, err := collectHeartbeatStatus(
				context.Background(),
				root,
				t.TempDir(),
				"concierge",
				"/unused",
			)
			if err != nil {
				t.Fatal(err)
			}
			if !status.Active || status.Notify != overdue {
				t.Fatalf("status = %+v", status)
			}
			pending := invoke(t, root, "", "task", "pending")
			for _, text := range []string{status.Text, pending} {
				if strings.Contains(
					text,
					"stuck_launches[1]{task,phase,session,pane_id,generation,deadline,recovery}:",
				) != overdue {
					t.Fatal(text)
				}
				if overdue {
					for _, want := range []string{"launching", "agent_requested", "work", "pane-1", "concierge task abandon", "--generation gen-1"} {
						if !strings.Contains(text, want) {
							t.Fatalf("missing %q: %s", want, text)
						}
					}
				}
			}
		})
	}
}

func TestHeartbeatSchedule(t *testing.T) {
	for _, trial := range []struct {
		name     string
		active   bool
		interval time.Duration
		wake     string
		elapsed  time.Duration
		want     string
	}{
		{"worker interval", true, 5 * time.Minute, "checkpoint", 5 * time.Minute, "status: due"},
		{"before interval", true, 5 * time.Minute, "checkpoint", 4 * time.Minute, "status: quiet"},
		{"no workers", false, 5 * time.Minute, "checkpoint", 5 * time.Minute, "status: quiet"},
		{"configured interval", true, time.Minute, "checkpoint", time.Minute, "status: due"},
		{"inbox wake", true, time.Minute, "inbox", time.Minute, "status: quiet"},
		{"reminder wake", true, time.Minute, "reminder", time.Minute, "status: quiet"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
			status := heartbeatStatus{
				Active: trial.active,
				Text:   "workers: 1\nworker: finished, idle\n",
				Notify: true,
			}
			check := func(at time.Time, wake string) string {
				t.Helper()
				var out bytes.Buffer
				if err := claimHeartbeat(context.Background(), root, trial.interval, at, wake, status, &out); err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			if got := check(now, "checkpoint"); !strings.Contains(got, "status: quiet") {
				t.Fatal(got)
			}
			got := check(now.Add(trial.elapsed), trial.wake)
			if !strings.Contains(got, trial.want) {
				t.Fatal(got)
			}
			if strings.Contains(got, "status: due") &&
				(!strings.Contains(got, "finished, idle") || !strings.Contains(got, "PushNotification")) {
				t.Fatal(got)
			}
			if strings.Contains(got, "status: due") {
				for _, want := range []string{"Start with Waiting on you.", "Keep context brief and put it last."} {
					if !strings.Contains(got, want) {
						t.Fatalf("missing %q: %s", want, got)
					}
				}
			}
			if got := check(now.Add(trial.elapsed), "checkpoint"); !strings.Contains(
				got,
				"status: quiet",
			) {
				t.Fatal(got)
			}
			if trial.active {
				got := check(now.Add(trial.elapsed+trial.interval), "checkpoint")
				if !strings.Contains(got, "status: due") {
					t.Fatal(got)
				}
				if trial.want == "status: due" &&
					!strings.Contains(got, "no changes since last heartbeat") {
					t.Fatal(got)
				}
			}
		})
	}
}

func TestHeartbeatConfiguration(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	if got := invoke(t, root, "", "heartbeat", "show"); !strings.Contains(got, "interval: 5m0s") {
		t.Fatal(got)
	}
	invoke(t, root, "", "heartbeat", "set", "--interval", "2m")
	if got := invoke(t, root, "", "heartbeat", "show"); !strings.Contains(got, "interval: 2m0s") {
		t.Fatal(got)
	}
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"--root", root, "heartbeat", "set", "--interval", "0s"}, strings.NewReader(""), &out, &out); code != 2 {
		t.Fatal(code, out.String())
	}
}

func TestHeartbeatReportsIdleFinishedWorker(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	executable, calls := controlFixture(t)
	worker := &taskstate.Worker{
		Name: "worker", Harness: "codex", Session: "work", Pane: "pane-1",
		Workspace: "workspace-1", Tab: "tab:1", Project: "/repo", Worktree: "/worktree",
		Generation: "gen-1", AgentArgs: []string{"resume", "thread-1"},
	}
	seedViewTask(t, root, "finished", taskstate.InFlight, worker, nil)
	messageRoot := t.TempDir()
	initial, err := collectHeartbeatStatus(
		context.Background(),
		root,
		messageRoot,
		"concierge",
		executable,
	)
	if err != nil || initial.Notify {
		t.Fatal(
			"idle worker without a result must not request a finished-worker notification",
			initial,
			err,
		)
	}
	messages, _ := inbox.New(messageRoot)
	recipient, _ := inbox.ParseRecipient("concierge")
	message, err := messages.Send(
		recipient,
		"task-reply:"+root+":finished:gen-1:assignment",
		"Finished; approve the result",
	)
	if err != nil {
		t.Fatal(err)
	}
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
	if !status.Active || !status.Notify {
		t.Fatalf("idle result lacks notification: %+v", status)
	}
	for _, want := range []string{"workers: 1 bound", "worker: \"finished\", idle", "pending_replies: 1", "operator_actions: 0 open"} {
		if !strings.Contains(status.Text, want) {
			t.Fatal(status.Text)
		}
	}
	data, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(data), "work|||agent get pane-1") {
		t.Fatalf("status used wrong session: %s, %v", data, err)
	}
	pending, err := messages.List(recipient)
	if err != nil || len(pending) != 1 || pending[0].ID != message.ID {
		t.Fatal("heartbeat changed pending replies", err)
	}
	if err := messages.Acknowledge(recipient, message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Send(recipient, "task-reply:"+root+":finished:gen-1:second", "A different result"); err != nil {
		t.Fatal(err)
	}
	changed, err := collectHeartbeatStatus(
		context.Background(),
		root,
		messageRoot,
		"concierge",
		executable,
	)
	if err != nil || changed.Text == status.Text {
		t.Fatal("replacement reply must count as changed status", err)
	}

	completion := &taskstate.Completion{
		Generation: "gen-1",
		Mode:       taskstate.Retain,
		Phase:      taskstate.Accepted,
	}
	seedViewTask(t, root, "finished", taskstate.InFlight, worker, completion)
	invoke(t, root, "", "operator", "ask", "approve", "Approve the delivery")
	status, err = collectHeartbeatStatus(
		context.Background(),
		root,
		messageRoot,
		"concierge",
		executable,
	)
	if err != nil || !strings.Contains(status.Text, "awaiting_cleanup: 1") ||
		!strings.Contains(status.Text, "Approve the delivery") {
		t.Fatal(status, err)
	}
}

func TestHeartbeatResetsAfterWorkEnds(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	now := time.Now()
	for _, trial := range []struct {
		elapsed time.Duration
		active  bool
		want    string
	}{
		{0, true, "status: quiet"},
		{time.Minute, false, "active: no"},
		{time.Hour, true, "status: quiet"},
		{time.Hour + 5*time.Minute, true, "status: due"},
	} {
		var out bytes.Buffer
		if err := claimHeartbeat(context.Background(), root, 5*time.Minute, now.Add(trial.elapsed), "checkpoint", heartbeatStatus{Active: trial.active}, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), trial.want) {
			t.Fatal(out.String())
		}
	}
}

func TestHeartbeatRejectsCorruptTimer(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "heartbeat.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := claimHeartbeat(context.Background(), root, time.Minute, time.Now(), "checkpoint", heartbeatStatus{Active: true}, &out); err == nil {
		t.Fatal("accepted corrupt timer")
	}
}

func TestHeartbeatCheckpointUsesConfigurationAndDefersOtherWakes(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	messageRoot := t.TempDir()
	check := func(args ...string) string {
		t.Helper()
		return invoke(
			t,
			root,
			"",
			append([]string{"--message-root", messageRoot, "heartbeat", "check"}, args...)...)
	}
	if got := check(); !strings.Contains(got, "active: no") {
		t.Fatal(got)
	}
	invoke(t, root, "", "operator", "ask", "approval", "Approve the result")
	invoke(t, root, "", "heartbeat", "set", "--interval", "1m")
	setLastWake := func() {
		t.Helper()
		data := `{"last_wake":"` + time.Now().
			Add(-2*time.Minute).
			UTC().
			Format(time.RFC3339Nano) +
			`"}`
		if err := os.WriteFile(filepath.Join(root, "heartbeat.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setLastWake()
	if got := check(); !strings.Contains(got, "status: due") ||
		!strings.Contains(got, "Approve the result") {
		t.Fatal(got)
	}
	for _, wake := range []string{"inbox", "reminder"} {
		setLastWake()
		if got := check("--wake", wake); !strings.Contains(got, "status: quiet") {
			t.Fatal(got)
		}
		if got := check(); !strings.Contains(got, "status: quiet") {
			t.Fatal(got)
		}
	}
}
