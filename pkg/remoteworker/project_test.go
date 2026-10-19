package remoteworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func remoteProjectFixture(t *testing.T) (Worktrees, taskstate.RemoteLease) {
	t.Helper()
	return remoteProjectAt(t, t.TempDir())
}

func remoteProjectAt(t *testing.T, base string) (Worktrees, taskstate.RemoteLease) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "machine-owner"), []byte("machine-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seed := t.TempDir()
	remoteProjectGit(t, git, seed, "init", "-b", "main")
	remoteProjectGit(
		t,
		git,
		seed,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Source",
	)
	if err := os.Mkdir(filepath.Join(base, "repositories"), 0700); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(base, "repositories", "repo.git")
	remoteProjectGit(t, git, base, "clone", "--bare", seed, bare)
	remoteProjectGit(t, git, bare, "fetch", "origin", "+refs/heads/*:refs/remotes/origin/*")
	remoteProjectGit(t, git, bare, "remote", "set-head", "origin", "--auto")
	location := taskstate.RemoteLease{
		Machine:        "machine-1",
		Target:         "dev-remote",
		PrimaryRoot:    "/primary/metadata-only",
		PrimaryProject: "/primary/never-open",
		Base:           base,
		Repository:     "repo",
		ClientCommit:   strings.Repeat("b", 40),
	}
	return Worktrees{Git: worktree.Client{Executable: git}}, location
}

func TestRemoteProjectCreatesLinkedControlCheckoutWithoutPrimaryPaths(t *testing.T) {
	manager, location := remoteProjectFixture(t)
	for range 2 {
		project, err := manager.PrepareProject(context.Background(), location)
		if err != nil || project != location.ProjectPath() {
			t.Fatalf("project=%s error=%v", project, err)
		}
		common := remoteProjectGit(
			t,
			manager.Git.Executable,
			project,
			"rev-parse",
			"--path-format=absolute",
			"--git-common-dir",
		)
		if common != filepath.Join(location.Base, "repositories", "repo.git") {
			t.Fatal("control checkout belongs to another repository")
		}
	}
	if err := os.WriteFile(filepath.Join(location.ProjectPath(), "keep"), []byte("operator data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PrepareProject(context.Background(), location); err == nil {
		t.Fatal("used a control checkout with uncommitted files")
	}
	data, err := os.ReadFile(filepath.Join(location.ProjectPath(), "keep"))
	if err != nil || string(data) != "operator data" {
		t.Fatal("discarded operator files")
	}
}

func TestRemoteProjectRejectsForeignMachineAndSymbolicControl(t *testing.T) {
	for _, kind := range []string{"foreign machine", "symbolic control", "foreign directory"} {
		t.Run(kind, func(t *testing.T) {
			manager, location := remoteProjectFixture(t)
			if kind == "foreign machine" {
				location.Machine = "other-machine"
			} else {
				if err := os.Mkdir(filepath.Join(location.Base, "projects"), 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "symbolic control" {
					if err := os.Symlink(t.TempDir(), location.ProjectPath()); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(location.ProjectPath(), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := manager.PrepareProject(context.Background(), location); err == nil {
				t.Fatal("claimed a foreign control checkout")
			}
		})
	}
}

func TestRemoteProjectRefusesIncompletePreparedRepository(t *testing.T) {
	manager, location := remoteProjectFixture(t)
	bare := filepath.Join(location.Base, "repositories", "repo.git")
	remoteProjectGit(
		t,
		manager.Git.Executable,
		bare,
		"symbolic-ref",
		"--delete",
		"refs/remotes/origin/HEAD",
	)
	if _, err := manager.PrepareProject(context.Background(), location); err == nil {
		t.Fatal("created a control checkout without a discoverable default branch")
	}
	if _, err := os.Lstat(location.ProjectPath()); !os.IsNotExist(err) {
		t.Fatal("mutated an incompletely prepared repository")
	}
}

func TestRemoteProjectRefusesSymbolicOwnershipLock(t *testing.T) {
	manager, location := remoteProjectFixture(t)
	if err := os.Symlink("machine-owner", filepath.Join(location.Base, ".project-lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PrepareProject(context.Background(), location); err == nil {
		t.Fatal("accepted a lock alias to another private file")
	}
}

func remoteProjectGit(t *testing.T, git, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command(git, append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", arguments, output, err)
	}
	return strings.TrimSpace(string(output))
}
