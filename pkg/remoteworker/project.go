package remoteworker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/roman/concierge/pkg/gitcommand"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/worktree"
)

var ErrProjectOwnership = errors.New(
	"remote project ownership is uncertain; retain the existing files",
)

type Worktrees struct {
	Git        worktree.Client
	Treehouse  string
	CheckQuiet func(context.Context, string) error
}

// PrepareProject supplies a working-directory checkout for Treehouse's repository discovery.
// Primary paths in location remain metadata and are never opened here.
func (manager Worktrees) PrepareProject(
	ctx context.Context,
	location taskstate.RemoteLease,
) (string, error) {
	if location == (taskstate.RemoteLease{}) || location.Validate() != nil ||
		!filepath.IsAbs(manager.Git.Executable) {
		return "", ErrProjectOwnership
	}
	canonical, err := filepath.EvalSymlinks(location.Base)
	if err != nil || canonical != location.Base {
		return "", ErrProjectOwnership
	}
	root, err := workeraccess.OpenPrivateDirectory(location.Base)
	if err != nil {
		return "", ErrProjectOwnership
	}
	defer root.Close()
	if !projectMachineOwner(root, location.Machine) {
		return "", ErrProjectOwnership
	}
	// Serialize control checkout creation without sharing the task's generation lock.
	lock, err := root.OpenFile(
		".project-lock",
		os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK,
		0600,
	)
	if err != nil {
		return "", ErrProjectOwnership
	}
	defer lock.Close()
	selected, err := root.Lstat(".project-lock")
	if err != nil || !privateProjectFile(selected) {
		return "", ErrProjectOwnership
	}
	if info, err := lock.Stat(); err != nil || !os.SameFile(info, selected) {
		return "", ErrProjectOwnership
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return "", ErrProjectOwnership
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	bare := filepath.Join(location.Base, "repositories", location.Repository+".git")
	if resolved, err := filepath.EvalSymlinks(bare); err != nil || resolved != bare {
		return "", ErrProjectOwnership
	}
	if kind, err := manager.projectGit(ctx, bare, "rev-parse", "--is-bare-repository"); err != nil ||
		kind != "true" {
		return "", ErrProjectOwnership
	}
	if common, err := manager.projectGit(ctx, bare, "rev-parse", "--path-format=absolute", "--git-common-dir"); err != nil ||
		common != bare {
		return "", ErrProjectOwnership
	}
	if branch, err := manager.projectGit(ctx, bare, "symbolic-ref", "refs/remotes/origin/HEAD"); err != nil ||
		!strings.HasPrefix(branch, "refs/remotes/origin/") {
		return "", ErrProjectOwnership
	}
	if err := root.Mkdir("projects", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", ErrProjectOwnership
	}
	projects := filepath.Join(location.Base, "projects")
	if resolved, err := filepath.EvalSymlinks(projects); err != nil || resolved != projects {
		return "", ErrProjectOwnership
	}
	private, err := workeraccess.OpenPrivateDirectory(projects)
	if err != nil {
		return "", ErrProjectOwnership
	}
	private.Close()
	project := location.ProjectPath()
	if _, err := os.Lstat(project); errors.Is(err, os.ErrNotExist) {
		if _, err := manager.projectGit(ctx, bare, "worktree", "add", "--detach", project, "refs/remotes/origin/HEAD"); err != nil {
			return "", ErrProjectOwnership
		}
	} else if err != nil {
		return "", ErrProjectOwnership
	}
	if resolved, err := filepath.EvalSymlinks(project); err != nil || resolved != project {
		return "", ErrProjectOwnership
	}
	if top, err := manager.projectGit(ctx, project, "rev-parse", "--show-toplevel"); err != nil ||
		top != project {
		return "", ErrProjectOwnership
	}
	if common, err := manager.projectGit(ctx, project, "rev-parse", "--path-format=absolute", "--git-common-dir"); err != nil ||
		common != bare {
		return "", ErrProjectOwnership
	}
	if status, err := manager.projectGit(ctx, project, "status", "--porcelain=v1", "--untracked-files=all", "--ignored"); err != nil ||
		status != "" {
		return "", ErrProjectOwnership
	}
	return project, nil
}

func projectMachineOwner(root *os.Root, machine string) bool {
	before, err := root.Lstat("machine-owner")
	if err != nil || !privateProjectFile(before) {
		return false
	}
	file, err := root.OpenFile(
		"machine-owner",
		os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil || string(data) != machine+"\n" {
		return false
	}
	after, err := root.Lstat("machine-owner")
	return err == nil && os.SameFile(before, after)
}

func privateProjectFile(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == uint32(os.Geteuid()) && info.Mode().IsRegular() &&
		info.Mode().Perm() == 0600
}

func (manager Worktrees) projectGit(
	ctx context.Context,
	directory string,
	arguments ...string,
) (string, error) {
	command := exec.CommandContext(
		ctx,
		manager.Git.Executable,
		gitcommand.Arguments(directory, arguments...)...)
	command.Env = sshcommand.ConnectionEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return "", ErrProjectOwnership
	}
	return strings.TrimSpace(string(output)), nil
}
