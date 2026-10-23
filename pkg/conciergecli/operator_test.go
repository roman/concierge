package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestOperatorActionLifecycle(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	for _, trial := range []struct {
		args []string
		want string
	}{
		{[]string{"operator", "list"}, "operator_actions: 0 open"},
		{[]string{"operator", "ask", "stop-worker", "Approve stopping the worker"}, "Approve stopping the worker"},
		{[]string{"operator", "ask", "stop-worker", "Approve stopping the worker"}, "operator_actions: 1 open"},
		{[]string{"task", "pending"}, "Approve stopping the worker"},
		{[]string{"--message-root", t.TempDir(), "brief"}, "Approve stopping the worker"},
		{[]string{"operator", "resolve", "stop-worker"}, "operator_actions: 0 open"},
		{[]string{"operator", "resolve", "stop-worker"}, "operator_actions: 0 open"},
	} {
		got := invoke(t, root, "", trial.args...)
		if !strings.Contains(got, trial.want) {
			t.Fatalf("%v: missing %q in %s", trial.args, trial.want, got)
		}
	}
}

func TestOperatorListRequiresInitializedState(t *testing.T) {
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"--root", t.TempDir(), "operator", "list"},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code == 0 {
		t.Fatalf("uninitialized state accepted: %s", &output)
	}
}

func TestOperatorAskRejectsChangedRequestAndCorruptLedger(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	invoke(t, root, "", "operator", "ask", "approval", "Approve the stop")
	for _, trial := range []struct {
		name    string
		args    []string
		prepare func()
	}{
		{"changed request", []string{"operator", "ask", "approval", "Approve the merge"}, func() {}},
		{"blank request", []string{"operator", "ask", "blank", " "}, func() {}},
		{"invalid interval", []string{"operator", "remind", "--interval", "0s"}, func() {}},
		{"corrupt ledger", []string{"operator", "remind"}, func() {
			if err := os.WriteFile(filepath.Join(root, "operator-actions.json"), []byte("broken"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(trial.name, func(t *testing.T) {
			trial.prepare()
			var output bytes.Buffer
			args := append([]string{"--root", root}, trial.args...)
			if code := Run(context.Background(), args, strings.NewReader(""), &output, &output); code == 0 {
				t.Fatal(output.String())
			}
		})
	}
}

func TestOperatorReminderTracksItemsSeparately(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	invoke(t, root, "", "operator", "ask", "first", "First request")
	invoke(t, root, "", "operator", "remind")
	invoke(t, root, "", "operator", "ask", "first", "First request")
	invoke(t, root, "", "operator", "ask", "second", "Second request")
	got := invoke(t, root, "", "operator", "remind")
	if !strings.Contains(got, "Second request") || strings.Contains(got, "First request") {
		t.Fatal(got)
	}
	invoke(t, root, "", "operator", "resolve", "first")
	invoke(t, root, "", "operator", "ask", "first", "Replacement request")
	got = invoke(t, root, "", "operator", "remind")
	if !strings.Contains(got, "Replacement request") || strings.Contains(got, "Second request") {
		t.Fatal(got)
	}
}

func TestOperatorViewsReportCorruptLedger(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "operator-actions.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"operator", "list"}, {"task", "pending"}, {"--message-root", t.TempDir(), "brief"}} {
		var output bytes.Buffer
		code := Run(
			context.Background(),
			append([]string{"--root", root}, args...),
			strings.NewReader(""),
			&output,
			&output,
		)
		if code == 0 || !strings.Contains(output.String(), "error:") {
			t.Fatalf("%v code %d: %s", args, code, &output)
		}
	}
}

func TestOperatorReminderRateLimitAndResolution(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	invoke(t, root, "", "operator", "ask", "approval", "Approve the stop command")
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	for _, trial := range []struct {
		elapsed time.Duration
		want    string
	}{
		{0, "status: due"},
		{time.Minute, "status: quiet"},
		{15 * time.Minute, "status: due"},
	} {
		var output bytes.Buffer
		if code := runOperatorReminder(context.Background(), root, 15*time.Minute, now.Add(trial.elapsed), &output); code != 0 {
			t.Fatal(output.String())
		}
		if !strings.Contains(output.String(), trial.want) {
			t.Fatalf("after %s: %s", trial.elapsed, &output)
		}
		if trial.want == "status: due" {
			for _, want := range []string{"Start the reply with a short Waiting on you list.", "Keep context brief and put it last.", "PushNotification"} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q: %s", want, &output)
				}
			}
		}
	}
	invoke(t, root, "", "operator", "resolve", "approval")
	got := invoke(t, root, "", "operator", "remind")
	if !strings.Contains(got, "status: quiet") ||
		strings.Contains(got, "Approve the stop command") {
		t.Fatal(got)
	}
}

func TestOperatorRemindersIncludeAcceptedWorkerUntilCleanup(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	worker := &taskstate.Worker{
		Name: "worker", Harness: "codex", Session: "work", Pane: "pane-1",
		Workspace: "workspace-1", Tab: "tab:1", Project: "/repo", Worktree: "/worktree",
		Generation: "gen-1", AgentArgs: []string{"resume", "thread-1"},
	}
	completion := &taskstate.Completion{
		Generation: "gen-1",
		Mode:       taskstate.Retain,
		Phase:      taskstate.Accepted,
	}
	seedViewTask(t, root, "finished", taskstate.InFlight, worker, completion)
	for _, args := range [][]string{{"task", "pending"}, {"--message-root", t.TempDir(), "brief"}, {"operator", "remind"}} {
		got := invoke(t, root, "", args...)
		if !strings.Contains(got, "finished") || !strings.Contains(got, "accepted") {
			t.Fatalf("%v: %s", args, got)
		}
	}
	completion.Phase = taskstate.Retired
	seedViewTask(t, root, "finished", taskstate.InFlight, nil, completion)
	got := invoke(t, root, "", "operator", "remind")
	if !strings.Contains(got, "status: quiet") {
		t.Fatal(got)
	}
	var later bytes.Buffer
	if code := runOperatorReminder(context.Background(), root, 15*time.Minute, time.Now().Add(15*time.Minute), &later); code != 0 ||
		!strings.Contains(later.String(), "retired") {
		t.Fatal(later.String())
	}
	seedViewTask(t, root, "finished", taskstate.Queued, nil, nil)
	got = invoke(t, root, "", "operator", "remind")
	if !strings.Contains(got, "status: quiet") {
		t.Fatal(got)
	}
}
