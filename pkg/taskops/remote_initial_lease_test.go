package taskops

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskstate"
)

type initialRemoteWorktrees struct {
	imports           int
	holders           []string
	loseFirstResponse bool
}

func (remote *initialRemoteWorktrees) ImportBase(
	_ context.Context,
	_ herdr.SavedMachine,
	_ remoteworker.WorktreeRequest,
	bundle io.Reader,
) error {
	remote.imports++
	count, err := io.Copy(io.Discard, bundle)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("empty committed base")
	}
	return nil
}

func (remote *initialRemoteWorktrees) Acquire(
	_ context.Context,
	_ herdr.SavedMachine,
	request remoteworker.WorktreeRequest,
) (taskstate.WorktreeLease, error) {
	remote.holders = append(remote.holders, request.Holder)
	if remote.loseFirstResponse && len(remote.holders) == 1 {
		return taskstate.WorktreeLease{}, errors.New("response lost")
	}
	location := request.Location
	location.Backend = taskstate.RemoteGit
	return taskstate.WorktreeLease{
		Remote:     location,
		ID:         "captured-remote-lease",
		Holder:     request.Holder,
		Project:    location.ProjectPath(),
		Path:       location.PoolRoot() + "/slot/project",
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
		State:      taskstate.LeaseHeld,
	}, nil
}

func (remote *initialRemoteWorktrees) PrepareLaunchSite(
	_ context.Context,
	_ herdr.SavedMachine,
	request remoteworker.LaunchSiteRequest,
) (remoteworker.LaunchSite, error) {
	tools, err := request.Tools()
	return remoteworker.LaunchSite{
		Lease:       request.Lease,
		Tools:       tools,
		Environment: herdr.PlainWorktree,
	}, err
}

func remoteInitialLeaseFixture(
	t *testing.T,
) (*Service, *taskstate.LaunchIntent, InitialLaunchConfig, *initialRemoteWorktrees) {
	t.Helper()
	service, runner, config := initialLaunchFixture(t)
	ctx := context.Background()
	if _, err := service.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.CancelInitialLaunch("gen-1") }); err != nil {
		t.Fatal(err)
	}
	upgrade, err := stateversion.AcquireUpgrade(ctx, service.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgrade.PublishVersion(3); err != nil {
		upgrade.Close()
		t.Fatal(err)
	}
	if err := upgrade.Close(); err != nil {
		t.Fatal(err)
	}
	intent := runner.intent
	intent.Remote = taskstate.RemoteLease{
		Machine:        "remote-machine",
		Target:         "saved-target",
		PrimaryRoot:    service.root,
		PrimaryProject: intent.Project,
		Base:           "/r",
		Repository:     "project",
		ClientCommit:   strings.Repeat("b", 40),
	}
	intent.Machine = intent.Remote.Machine
	intent.Project = intent.Remote.ProjectPath()
	intent.Worktree = ""
	if _, err := service.store.ReserveInitialLaunch(ctx, "task-1", intent); err != nil {
		t.Fatal(err)
	}
	task, err := service.store.Update(
		ctx,
		"task-1",
		func(task taskstate.Task) (taskstate.Task, error) {
			return task.BeginLaunchPreparation(intent.Generation)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	remote := &initialRemoteWorktrees{}
	config.Remote = &RemoteInitialLaunchConfig{
		Profile: herdr.SavedMachine{
			ID:      intent.Machine,
			Target:  intent.Remote.Target,
			Session: intent.Session,
			Enabled: true,
		},
		Worktrees: remote,
	}
	return service, task.Snapshot().LaunchIntent, config, remote
}

func TestInitialRemoteLeaseCapturesAndRetainsItsHolder(t *testing.T) {
	for _, lose := range []bool{false, true} {
		service, intent, config, remote := remoteInitialLeaseFixture(t)
		remote.loseFirstResponse = lose
		ctx := context.Background()
		if lose {
			if _, err := service.prepareRemoteInitialLease(ctx, "task-1", intent, config); err == nil {
				t.Fatal("confirmed a lost response")
			}
			task, err := service.store.Read(ctx, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			intent = task.Snapshot().LaunchIntent
			if intent.Phase != taskstate.LaunchLeaseRequested || intent.LeaseHolder == "" ||
				intent.Worktree != "" {
				t.Fatal("lost the pending acquisition identity")
			}
		}
		captured, err := service.prepareRemoteInitialLease(ctx, "task-1", intent, config)
		if err != nil {
			t.Fatal(err)
		}
		if captured.Phase != taskstate.LaunchLeaseReady ||
			captured.Lease.ID != "captured-remote-lease" ||
			captured.Lease.Remote.PrimaryProject == "" {
			t.Fatal("lost remote routing during capture")
		}
		for _, holder := range remote.holders {
			if holder != captured.LeaseHolder {
				t.Fatal("changed holder across recovery")
			}
		}
		before := len(remote.holders)
		if _, err := service.prepareRemoteInitialLease(ctx, "task-1", captured, config); err != nil {
			t.Fatal(err)
		}
		if len(remote.holders) != before {
			t.Fatal("repeated a captured acquisition")
		}
	}
}

func TestInitialRemoteLeaseRejectsChangedProfileBeforeEffects(t *testing.T) {
	service, intent, config, remote := remoteInitialLeaseFixture(t)
	config.Remote.Profile.Target = "another-target"
	if _, err := service.prepareRemoteInitialLease(context.Background(), "task-1", intent, config); err == nil {
		t.Fatal("followed a changed saved destination")
	}
	if remote.imports != 0 || len(remote.holders) != 0 {
		t.Fatal("contacted the changed destination")
	}
}
