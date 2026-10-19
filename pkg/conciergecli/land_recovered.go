package conciergecli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type landRecoveredCommand struct {
	TaskID           string `arg:"" help:"Closed task with the latest returned local lease receipt."`
	Generation       string `required:"" help:"Exact generation in the task receipt."`
	Commit           string `required:"" help:"Full reviewed worker commit ID; must equal worktree HEAD."`
	Treehouse        string `default:"treehouse" help:"Treehouse executable used to verify the returned slot."`
	OperatorApproved bool   `help:"Confirm explicit operator approval for this exact recovery landing."`
}

func (command *landRecoveredCommand) run(ctx context.Context, root string, output io.Writer) int {
	if !command.OperatorApproved {
		return fail(
			output,
			1,
			errors.New("recovery landing requires explicit operator approval"),
			"obtain approval for the exact task, generation, and commit; then pass --operator-approved",
		)
	}
	if !validRecoveredCommit(command.Commit) {
		return fail(output, 1, errors.New("--commit must be a full lowercase Git commit ID"),
			"inspect the reviewed worker commit")
	}
	treehouse, err := exec.LookPath(command.Treehouse)
	if err == nil {
		treehouse, err = filepath.Abs(treehouse)
	}
	if err != nil {
		return fail(output, 1, err, "provide an installed Treehouse executable")
	}
	activity, err := hostactivity.Begin(root)
	if err != nil {
		return fail(output, 1, err, "wait for the host restart before landing")
	}
	defer activity.Close()
	store, err := taskstate.NewStore(root)
	if err != nil {
		return fail(output, 1, err, "inspect the task state root")
	}
	var target landing
	var result landingResult
	landed := false
	_, err = store.WithTask(
		ctx,
		command.TaskID,
		func(task *taskstate.LockedTask) (landingErr error) {
			snapshot := task.Current().Snapshot()
			if snapshot.Definition.Kind == "scout" || snapshot.State != taskstate.Done ||
				snapshot.Worker != nil || snapshot.Completion != nil || snapshot.LaunchIntent != nil ||
				snapshot.Receipt == nil || snapshot.Receipt.Mode != taskstate.Close ||
				snapshot.Receipt.Generation != command.Generation {
				return errors.New(
					"recovery landing requires the latest closed task receipt for this generation",
				)
			}
			if snapshot.Definition.Hold != nil {
				return errors.New("release the task hold before landing")
			}
			lease := snapshot.Receipt.Lease
			if lease.State != taskstate.LeaseReturned ||
				lease.Remote != (taskstate.RemoteLease{}) ||
				lease.Holder != command.TaskID ||
				lease.Project == "" ||
				lease.Path == "" ||
				lease.BaseCommit == "" {
				return errors.New(
					"recovery landing requires this task's returned local worktree lease",
				)
			}
			settings, err := readProjectSettings(
				filepath.Join(root, "config", "projects.json"),
				snapshot.Definition.Repo,
			)
			if err != nil {
				return err
			}
			if settings.Delivery != "fast-forward" {
				return errors.New("recovery landing supports fast-forward delivery only")
			}
			pool := worktree.PoolClient{Executable: treehouse}
			holder := "concierge-land-recovered:" + command.TaskID + ":" + command.Generation + ":" + command.Commit
			reservation, err := pool.ReserveReturned(ctx, lease.Project, lease.Path, holder)
			if reservation.ID != "" && reservation.Path == lease.Path {
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if releaseErr := pool.Return(cleanup, lease.Project, reservation); releaseErr != nil {
						message := fmt.Sprintf("recovery lease %s needs release", reservation.ID)
						if landed {
							message = fmt.Sprintf("commit %s landed; %s", result.commit, message)
						}
						landingErr = errors.Join(
							landingErr,
							fmt.Errorf("%s: %w", message, releaseErr),
						)
					}
				}()
			}
			if err != nil {
				return err
			}
			source := landingSource{
				project:  lease.Project,
				worktree: lease.Path,
				lease:    lease,
				commit:   command.Commit,
			}
			target, err = prepareLandingSource(ctx, source, taskstate.LeaseReturned)
			if err != nil {
				return err
			}
			base, err := landingGitCommand(
				ctx,
				target.project,
				"rev-parse",
				"--verify",
				lease.BaseCommit+"^{commit}",
			)
			if err != nil || base != lease.BaseCommit {
				return errors.New("recorded base commit is missing or changed")
			}
			if target.head == base {
				return errors.New("reviewed result contains no commit after the recorded base")
			}
			if _, err := landingGitCommand(ctx, target.project, "merge-base", "--is-ancestor", base, target.head); err != nil {
				return errors.New("reviewed result does not contain the recorded base commit")
			}
			if err := task.CheckCurrent(); err != nil {
				return err
			}
			if err := pool.ConfirmReservation(ctx, lease.Project, reservation); err != nil {
				return err
			}
			if err := requireCleanLandingTree(ctx, lease.Path); err != nil {
				return err
			}
			result, err = target.fastForward(ctx)
			landed = err == nil
			return err
		},
	)
	if landed {
		if actionErr := target.recordCheckoutAction(ctx, root, result.checkout); actionErr != nil {
			err = errors.Join(
				err,
				fmt.Errorf(
					"commit %s landed; cannot record checkout action: %w",
					result.commit,
					actionErr,
				),
			)
		}
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"inspect the receipt, reviewed commit, returned Treehouse slot, and remote before retrying",
		)
	}
	_, err = fmt.Fprintf(
		output,
		"task: %s\nstatus: landed\ndelivery: fast-forward\ndefault_branch: %s\ncommit: %s\ncheckout: %s\nremote_branch: %s\npr: %s\n",
		quote(
			command.TaskID,
		),
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

func validRecoveredCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
