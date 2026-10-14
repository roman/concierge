package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/roman/concierge/pkg/taskstate"
)

type landCommand struct {
	TaskID           string `arg:"" help:"Task with an accepted worker result and a recorded worktree lease."`
	OperatorApproved bool   `help:"Use approval given by the operator for this landing when merge is off."`
}

type landing struct {
	project, worktree, branch, defaultBranch, head, repository, remote string
}

type landingResult struct {
	checkout, remoteBranch, pullRequest, commit string
}

func (command *landCommand) run(ctx context.Context, root string, output io.Writer) int {
	store, err := taskstate.NewStore(root)
	if err != nil {
		return fail(output, 1, err, "inspect the task state root")
	}
	var target landing
	var result landingResult
	var delivery string
	_, err = store.WithTask(ctx, command.TaskID, func(task *taskstate.LockedTask) error {
		snapshot := task.Current().Snapshot()
		if snapshot.Definition.Kind == "scout" {
			return errors.New("scouts deliver reports and cannot land code")
		}
		if snapshot.Worker == nil || snapshot.Completion == nil ||
			snapshot.Completion.Phase != taskstate.Accepted ||
			snapshot.Completion.Generation != snapshot.Worker.Generation {
			return errors.New("landing requires an accepted worker result before cleanup")
		}
		if snapshot.Definition.Hold != nil {
			return errors.New("release the task hold before landing")
		}
		settings, err := readProjectSettings(
			filepath.Join(root, "config", "projects.json"),
			snapshot.Definition.Repo,
		)
		if err != nil {
			return err
		}
		if settings.Merge == "off" && !command.OperatorApproved {
			return errors.New(
				"merge is off. Obtain operator approval for this landing. Then pass --operator-approved",
			)
		}
		delivery = settings.Delivery
		target, err = prepareLanding(ctx, *snapshot.Worker)
		if err != nil {
			return err
		}
		if err := task.CheckCurrent(); err != nil {
			return err
		}
		if delivery == "pr" {
			result, err = target.mergePullRequest(ctx)
		} else {
			result, err = target.fastForward(ctx)
		}
		return err
	})
	if err != nil {
		return fail(
			output,
			1,
			err,
			"inspect the accepted task, project settings, and remote branch before retrying concierge land TASK_ID",
		)
	}
	_, err = fmt.Fprintf(
		output,
		"task: %s\nstatus: landed\ndelivery: %s\ndefault_branch: %s\ncommit: %s\ncheckout: %s\nremote_branch: %s\npr: %s\n",
		quote(
			command.TaskID,
		),
		delivery,
		quote(target.defaultBranch),
		quote(result.commit),
		quote(result.checkout),
		quote(result.remoteBranch),
		quote(result.pullRequest),
	)
	if err != nil {
		return 1
	}
	return 0
}

func runLandingCommand(
	ctx context.Context,
	directory, executable string,
	args ...string,
) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	data, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", executable, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func landingGitCommand(ctx context.Context, directory string, args ...string) (string, error) {
	return runLandingCommand(ctx, directory, "git", args...)
}

func prepareLanding(ctx context.Context, worker taskstate.Worker) (landing, error) {
	lease := worker.Lease
	if lease.Branch == "" || lease.Path != worker.Worktree || lease.Project != worker.Project ||
		lease.State != taskstate.LeaseHeld {
		return landing{}, errors.New("task has no held worktree lease with a recorded branch")
	}
	target := landing{project: worker.Project, worktree: worker.Worktree, branch: lease.Branch}
	var err error
	target.remote, err = matchingLandingEndpoints(ctx, target.project)
	if err != nil {
		return landing{}, err
	}
	if _, err := landingGitCommand(ctx, target.project, "check-ref-format", "--branch", target.branch); err != nil {
		return landing{}, err
	}
	projectCommon, err := landingGitCommand(
		ctx,
		target.project,
		"rev-parse",
		"--path-format=absolute",
		"--git-common-dir",
	)
	if err != nil {
		return landing{}, err
	}
	workerCommon, err := landingGitCommand(
		ctx,
		target.worktree,
		"rev-parse",
		"--path-format=absolute",
		"--git-common-dir",
	)
	if err != nil {
		return landing{}, err
	}
	projectCommon, err = filepath.EvalSymlinks(projectCommon)
	if err != nil {
		return landing{}, err
	}
	workerCommon, err = filepath.EvalSymlinks(workerCommon)
	if err != nil {
		return landing{}, err
	}
	if projectCommon != workerCommon {
		return landing{}, errors.New("recorded worktree belongs to another repository")
	}
	actual, err := landingGitCommand(ctx, target.worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return landing{}, err
	}
	expected, err := filepath.EvalSymlinks(target.worktree)
	if err != nil {
		return landing{}, err
	}
	if actual != expected {
		return landing{}, errors.New("recorded path is not the worktree root")
	}
	branch, err := landingGitCommand(ctx, target.worktree, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return landing{}, err
	}
	if branch != "refs/heads/"+target.branch {
		return landing{}, errors.New("worktree is not on the recorded branch")
	}
	if err := requireCleanLandingTree(ctx, target.worktree); err != nil {
		return landing{}, err
	}
	target.head, err = landingGitCommand(ctx, target.worktree, "rev-parse", "HEAD")
	if err != nil {
		return landing{}, err
	}
	remote, err := landingGitCommand(ctx, target.project, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return landing{}, err
	}
	for _, line := range strings.Split(remote, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" &&
			strings.HasPrefix(fields[1], "refs/heads/") {
			target.defaultBranch = strings.TrimPrefix(fields[1], "refs/heads/")
		}
	}
	if target.defaultBranch == "" {
		return landing{}, errors.New("remote origin does not advertise a default branch")
	}
	if target.branch == target.defaultBranch {
		return landing{}, errors.New("task branch must differ from the default branch")
	}
	remoteRef := "refs/remotes/origin/" + target.defaultBranch
	if _, err := landingGitCommand(ctx, target.project, "fetch", "origin", "refs/heads/"+target.defaultBranch+":"+remoteRef); err != nil {
		return landing{}, err
	}
	if _, err := landingGitCommand(ctx, target.project, "merge-base", "--is-ancestor", remoteRef, target.head); err != nil {
		return landing{}, errors.New(
			"task branch does not contain the remote default branch tip. Rebase before landing",
		)
	}
	return target, nil
}

func requireCleanLandingTree(ctx context.Context, directory string) error {
	status, err := landingGitCommand(
		ctx,
		directory,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
		"--ignored",
		"--ignore-submodules=none",
	)
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("worktree contains changed, untracked, or ignored files")
	}
	return nil
}

func (target landing) fastForward(ctx context.Context) (landingResult, error) {
	result := landingResult{
		checkout:     "skipped: checkout advancement not attempted",
		remoteBranch: "absent",
		commit:       target.head,
	}
	remote, err := matchingLandingEndpoints(ctx, target.project)
	if err != nil {
		return result, err
	}
	if remote != target.remote {
		return result, errors.New("origin changed during landing")
	}
	if _, err := landingGitCommand(ctx, target.project, "push", "origin", target.head+":refs/heads/"+target.defaultBranch); err != nil {
		return result, err
	}
	remote, err = matchingLandingEndpoints(ctx, target.project)
	if err != nil || remote != target.remote {
		result.remoteBranch = "skipped: origin changed after landing"
		result.checkout = target.advanceCheckout(ctx)
		return result, nil
	}
	remoteBranch, err := landingGitCommand(
		ctx,
		target.project,
		"ls-remote",
		"--heads",
		"origin",
		"refs/heads/"+target.branch,
	)
	if err != nil {
		result.remoteBranch = "skipped: cannot inspect remote task branch"
	} else if remoteBranch != "" {
		fields := strings.Fields(remoteBranch)
		// Preserve concurrent branch updates with an explicit deletion lease.
		if len(fields) != 2 || fields[0] != target.head {
			result.remoteBranch = "skipped: remote task branch differs from landed commit"
		} else if _, err := landingGitCommand(ctx, target.project, "push", "origin", "--delete", target.branch, "--force-with-lease=refs/heads/"+target.branch+":"+target.head); err != nil {
			result.remoteBranch = "skipped: remote task branch deletion failed"
		} else {
			result.remoteBranch = "deleted"
		}
	}
	result.checkout = target.advanceCheckout(ctx)
	return result, nil
}

func matchingLandingEndpoints(ctx context.Context, project string) (string, error) {
	fetchURL, err := landingGitCommand(ctx, project, "remote", "get-url", "--all", "origin")
	if err != nil {
		return "", err
	}
	pushURL, err := landingGitCommand(
		ctx,
		project,
		"remote",
		"get-url",
		"--push",
		"--all",
		"origin",
	)
	if err != nil {
		return "", err
	}
	if fetchURL == "" || strings.Contains(fetchURL, "\n") || fetchURL != pushURL {
		return "", errors.New("origin must have one matching fetch and push endpoint")
	}
	return fetchURL, nil
}

func (target landing) advanceCheckout(ctx context.Context) string {
	branch, err := landingGitCommand(ctx, target.project, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || branch != "refs/heads/"+target.defaultBranch {
		return "skipped: main checkout is not on the default branch"
	}
	if err := requireCleanLandingTree(ctx, target.project); err != nil {
		return "skipped: main checkout is dirty or unreadable"
	}
	if _, err := landingGitCommand(ctx, target.project, "merge", "--ff-only", target.head); err != nil {
		return "skipped: main checkout cannot fast-forward"
	}
	return "advanced"
}
