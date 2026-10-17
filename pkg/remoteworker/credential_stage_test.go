package remoteworker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

func credentialStageRequest(t *testing.T) LaunchSiteRequest {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	location := taskstate.RemoteLease{
		Machine:        "machine-1",
		Target:         "baker",
		PrimaryRoot:    root,
		PrimaryProject: "/primary/project-metadata",
		Base:           "/r",
		Repository:     "repo",
		ClientCommit:   strings.Repeat("b", 40),
	}
	request := WorktreeRequest{
		Task:       "task-one",
		Location:   location,
		Holder:     "stable-holder",
		Branch:     "task/result",
		BaseCommit: strings.Repeat("a", 40),
	}
	location.Backend = taskstate.RemoteGit
	lease := taskstate.WorktreeLease{
		Remote:     location,
		ID:         "lease-one",
		Holder:     request.Holder,
		Project:    location.ProjectPath(),
		Path:       location.PoolRoot() + "/slot/repo",
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
		State:      taskstate.LeaseHeld,
	}
	return LaunchSiteRequest{Worktree: request, Lease: lease, Generation: "generation-one"}
}

func TestPrimaryCredentialStageRecoversTheSamePrivateSecret(t *testing.T) {
	request := credentialStageRequest(t)
	root := request.Worktree.Location.PrimaryRoot
	grant, secret, err := workeraccess.Issue(
		request.Worktree.Task,
		request.Generation,
		workeraccess.Operations(),
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := OpenPrimaryCredentialStage(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.InstallSecret(secret); err != nil {
		stage.Close()
		t.Fatal(err)
	}
	if info, err := stage.root.Stat("secret"); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("staged secret is not private")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	stage, err = OpenPrimaryCredentialStage(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	data, err := stage.ReadSecret()
	if err != nil || !bytes.Equal(data, secret.Bytes()) {
		t.Fatal("lost the original setup secret")
	}
	if err := stage.RemoveSecret(strings.Repeat("0", 64)); err == nil {
		t.Fatal("removed a different credential")
	}
	if err := stage.RemoveSecret(grant.Verifier); err != nil {
		t.Fatal(err)
	}
	data, err = stage.ReadSecret()
	if err != nil || len(data) != 0 {
		t.Fatal("retained an acknowledged staging secret")
	}
}

func TestPrimaryCredentialStageRefusesReboundOrForeignStorage(t *testing.T) {
	for _, kind := range []string{"changed lease", "foreign owner", "symbolic namespace", "old root"} {
		t.Run(kind, func(t *testing.T) {
			request := credentialStageRequest(t)
			root := request.Worktree.Location.PrimaryRoot
			if kind == "symbolic namespace" {
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "remote-credentials")); err != nil {
					t.Fatal(err)
				}
			} else if kind == "old root" {
				if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("2\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				stage, err := OpenPrimaryCredentialStage(context.Background(), root, request)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "foreign owner" {
					file, err := stage.root.OpenFile("launch-owner.json", os.O_WRONLY|os.O_TRUNC, 0)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.WriteString("foreign"); err != nil {
						t.Fatal(err)
					}
					file.Close()
				} else {
					request.Lease.ID = "another-lease"
				}
				stage.Close()
			}
			if stage, err := OpenPrimaryCredentialStage(context.Background(), root, request); err == nil {
				stage.Close()
				t.Fatal("accepted unowned staging storage")
			}
		})
	}
}

func TestPrimaryCredentialStageKeepsSecretsWhenItsPathIsReplaced(t *testing.T) {
	request := credentialStageRequest(t)
	stage, err := OpenPrimaryCredentialStage(
		context.Background(),
		request.Worktree.Location.PrimaryRoot,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	grant, secret, err := workeraccess.Issue(
		request.Worktree.Task,
		request.Generation,
		workeraccess.Operations(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.InstallSecret(secret); err != nil {
		t.Fatal(err)
	}
	if err := stage.namespace.Rename(stage.name, stage.name+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := stage.namespace.Symlink(t.TempDir(), stage.name); err != nil {
		t.Fatal(err)
	}
	if err := stage.RemoveSecret(grant.Verifier); err == nil {
		t.Fatal("removed a secret after losing its storage identity")
	}
	data, err := workeraccess.ReadDirectorySecret(stage.root)
	if err != nil || !bytes.Equal(data, secret.Bytes()) {
		t.Fatal("discarded an unconfirmed setup secret")
	}
}

func TestPrimaryCredentialStageCannotSelectAnotherPrimaryRoot(t *testing.T) {
	request := credentialStageRequest(t)
	foreign := t.TempDir()
	if stage, err := OpenPrimaryCredentialStage(context.Background(), foreign, request); err == nil {
		stage.Close()
		t.Fatal("opened a caller-selected root")
	}
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != 0 {
		t.Fatal("mutated a foreign primary root")
	}
}
