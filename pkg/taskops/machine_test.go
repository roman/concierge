package taskops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

type remoteAdoptionRunner struct {
	runner *adoptionRunner
	calls  int
}

func (runner *remoteAdoptionRunner) RunMachine(
	ctx context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	if machine != "machine-1" || strings.Contains(strings.Join(args, " "), "--session") {
		return nil, nil, fmt.Errorf("wrong machine or local selection: %s %v", machine, args)
	}
	runner.calls++
	return runner.runner.RunSession(ctx, "work", args...)
}

func TestRemoteAdoptionAndObservationUseRemoteDirectory(t *testing.T) {
	service, input, local := adoptionFixture(t)
	service.workerCommands = CommandPaths{Concierge: "/tools/concierge", Tincan: "/tools/tincan"}
	if err := taskstate.UpgradeVersionTwo(context.Background(), service.root, service.root+"-backup"); err != nil {
		t.Fatal(err)
	}
	input.Machine = "machine-1"
	input.Worktree = "/Users/remote/concierge/task-1"
	local.worktree = input.Worktree
	remote := &remoteAdoptionRunner{runner: local}
	client := herdr.Client{MachineRunner: remote}
	task, err := service.AdoptWorker(context.Background(), input, client)
	if err != nil || task.Snapshot().Worker.Machine != input.Machine {
		t.Fatalf("adoption=%+v error=%v", task.Snapshot(), err)
	}
	if _, err := service.Start(context.Background(), input.TaskID); err != nil {
		t.Fatal(err)
	}
	worker, err := service.SupervisedWorker(input.TaskID, service.root+"-messages", client)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote.calls == 0 {
		t.Fatal("worker used no remote commands")
	}
	before, err := service.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	beforeData, _ := before.Encode()
	local.absent = true
	if err := worker.Restart(context.Background()); !errors.Is(err, ErrRemoteLaunchUnavailable) {
		t.Fatalf("restart without remote transport: %v", err)
	}
	after, err := service.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	afterData, _ := after.Encode()
	if !bytes.Equal(beforeData, afterData) || local.starts != 0 {
		t.Fatal("unsupported restart changed the remote binding")
	}
	local.worktree = "/Users/remote/other-task"
	if err := worker.Observe(context.Background()); err == nil {
		t.Fatal("worker accepted a different remote directory")
	}
}
