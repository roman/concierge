package hostservice

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestHostRemoteActivationRecoversStagingBeforeGrantPublication(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	stage, err := remoteworker.OpenPrimaryCredentialStage(
		context.Background(),
		fixture.server.config.Root,
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.InstallSecret(secret); err != nil {
		stage.Close()
		t.Fatal(err)
	}
	stage.Close()
	if err := fixture.activate(t); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.server.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Snapshot().Worker.Access.Verifier != grant.Verifier {
		t.Fatal("reissued a staged setup secret")
	}
}

func TestHostRemoteActivationNeverReissuesAnExistingGrant(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	grant, _, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.InstallWorkerAccess(grant) }); err != nil {
		t.Fatal(err)
	}
	if err := fixture.activate(t); err == nil {
		t.Fatal("recovered without the original remote or staged secret")
	}
	task, err := fixture.server.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Snapshot().Worker.Access.Verifier != grant.Verifier || fixture.ssh.installs != 0 ||
		fixture.processes.starts.Load() != 0 {
		t.Fatal("replaced an unavailable original credential")
	}
}

func TestHostRemoteActivationRejectsRevocationBeforeEffects(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	grant, _, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.InstallWorkerAccess(grant) }); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.RevokeWorkerAccess("gen-1") }); err != nil {
		t.Fatal(err)
	}
	if err := fixture.activate(t); err == nil {
		t.Fatal("activated a revoked generation")
	}
	if fixture.ssh.calls != 0 || fixture.processes.starts.Load() != 0 {
		t.Fatal("revoked access caused an external operation")
	}
	if _, err := os.Stat(filepath.Join(fixture.server.config.Root, "remote-credentials")); !os.IsNotExist(
		err,
	) {
		t.Fatal("revoked access created staging storage")
	}
}

func TestHostRemoteActivationRejectsMismatchedRepository(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	task, err := fixture.server.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := task.Snapshot()
	snapshot.Definition.Repo = "another-repository"
	changed, err := taskstate.NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := fixture.request.Tools()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.activationRequest("task-1", "gen-1", fixture.profile, tools, changed); err == nil {
		t.Fatal("activated a repository outside the assigned task")
	}
}
