package hostservice

import (
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestPartialRetirementRemovesStagingBeforeGrantPublication(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	fixture.server.preparationMachines = launchLocationCatalog{fixture.profile}
	stage, err := remoteworker.OpenPrimaryCredentialStage(
		t.Context(),
		fixture.server.config.Root,
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.InstallSecret(secret); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.server.store.WithTask(
		t.Context(),
		"task-1",
		func(locked *taskstate.LockedTask) error {
			return fixture.server.revokeRemoteWorkerTransportLocked(t.Context(), locked, "gen-1")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err = remoteworker.OpenPrimaryCredentialStage(
		t.Context(),
		fixture.server.config.Root,
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	staged, err := stage.ReadSecret()
	if err != nil || len(staged) != 0 || fixture.ssh.calls != 0 ||
		fixture.processes.starts.Load() != 0 {
		t.Fatalf("unpublished staging cleanup: %v", err)
	}
	retired, err := current.AbandonInitialLaunch("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := retired.Encode()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := taskstate.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := reloaded.LaunchAbandonment("gen-1")
	if !found || !receipt.WorkerRetired || receipt.Lease != fixture.request.Lease {
		t.Fatal("reload lost the held retirement lease")
	}
}

func TestBoundAbandonmentRequiresRevokedGrantAndUnfinishedLaunch(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	if err := fixture.activate(t); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.server.store.Read(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.AbandonInitialLaunch("gen-1"); !errors.Is(
		err,
		taskstate.ErrInvalidTransition,
	) {
		t.Fatalf("active grant: %v", err)
	}
	completed, err := current.CompleteInitialLaunch("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := completed.CheckAbandonInitialLaunch("gen-1"); !errors.Is(
		err,
		taskstate.ErrInvalidTransition,
	) {
		t.Fatalf("completed launch: %v", err)
	}
	current, err = current.RevokeWorkerAccess("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	retired, err := current.AbandonInitialLaunch("gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if retired.State() != taskstate.Queued || retired.Snapshot().Worker != nil {
		t.Fatal("retained bound worker")
	}
}
