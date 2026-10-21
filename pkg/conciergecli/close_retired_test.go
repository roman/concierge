package conciergecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestBacklogCloseRetiredTask(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	root := lifecycleRoot(t)
	claimNativeParent(t, root)
	store, err := taskstate.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(
		context.Background(),
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			task, err := task.Start()
			if err == nil {
				task, err = task.PlaceHold(taskstate.Hold{Reason: "Review", Kind: "primary"})
			}
			if err == nil {
				task, err = task.AcceptResult("gen-1", taskstate.Retain, "")
			}
			if err != nil {
				return taskstate.Task{}, err
			}
			return task.RetireResult("gen-1")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	serveConfiguredTaskRoot(
		t,
		hostservice.Config{
			Root:            root,
			MessageRoot:     filepath.Join(root, "messages"),
			Recipient:       recipient,
			HerdrExecutable: executable,
		},
	)
	args := []string{
		"backlog",
		"close",
		"task-1",
		"--generation",
		"gen-1",
		"--reason",
		"Superseded",
		"--closed-on",
		"2026-10-20",
	}
	for range 2 {
		got := invoke(t, root, "", args...)
		if !strings.Contains(got, "state: done") || !strings.Contains(got, `reason: "Superseded"`) {
			t.Fatal(got)
		}
	}
	got := invoke(t, root, "", "backlog", "show", "task-1")
	if !strings.Contains(got, `closure_reason: "Superseded"`) ||
		!strings.Contains(got, "closed: 2026-10-20") {
		t.Fatal(got)
	}
	args[6] = "Changed reason"
	expectLifecycleError(t, root, 1, args...)
}
