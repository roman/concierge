package taskops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
	"github.com/roman/concierge/pkg/worktree"
	"github.com/roman/tincan/pkg/inbox"
)

// DiscardDecisionRequired preserves the concrete inventory for operator review.
type DiscardDecisionRequired struct{ Inventory worktree.DiscardInventory }

func (failure *DiscardDecisionRequired) Error() string {
	inventory := failure.Inventory
	return fmt.Sprintf(
		"decision required; preserve worktree %q, branch %q, base %s, HEAD %s; files: %q; commits: %q",
		inventory.Worktree,
		inventory.Branch,
		inventory.BaseCommit,
		inventory.Head,
		inventory.Files,
		inventory.Commits,
	)
}

// DiscardWorker cancels a missing generation without accepting a result.
// The host must hold the task controller reservation throughout this operation.
func (service *Service) DiscardWorker(
	ctx context.Context,
	messageRoot, taskID, generation string,
	client herdr.Client,
) (taskstate.Task, error) {
	_, err := service.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if err := current.CheckDiscardWorker(generation); err != nil {
			return err
		}
		if receipt, found := current.LaunchAbandonment(generation); found &&
			receipt.Kind == taskstate.DiscardedWorker {
			return nil
		}
		worker := current.Snapshot().Worker
		if err := service.checkDiscardReports(messageRoot, taskID); err != nil {
			return err
		}
		target, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane)
		if err != nil {
			return err
		}
		if err := client.ConfirmPaneGone(ctx, target); err != nil {
			return err
		}
		scoped, err := client.ForEndpoint(worker.Machine, worker.Session)
		if err != nil {
			return err
		}
		if err := scoped.CheckLaunchAbsent(ctx, herdr.LaunchResources{Name: worker.Name, Label: worker.OwnedWorkspaceLabel, Tab: worker.Tab, Pane: worker.Pane}); err != nil {
			return err
		}
		if err := checkWorktreeLease(ctx, worker.Lease, service.pool); err != nil {
			return err
		}
		if err := service.checkDiscardContents(ctx, worker.Lease); err != nil {
			return err
		}
		if err := locked.CheckCurrent(); err != nil {
			return err
		}
		_, err = locked.Apply(
			func(task taskstate.Task) (taskstate.Task, error) { return task.DiscardWorker(generation) },
		)
		return err
	})
	if err != nil {
		if errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
			err = errors.Join(err, taskview.Rebuild(ctx, service.root))
		}
		return taskstate.Task{}, err
	}
	updated, returnErr := service.ReleaseWorktree(ctx, taskID, generation)
	if returnErr != nil {
		return taskstate.Task{}, errors.Join(returnErr, taskview.Rebuild(ctx, service.root))
	}
	return updated, nil
}

func (service *Service) checkDiscardContents(
	ctx context.Context,
	lease taskstate.WorktreeLease,
) error {
	inventory, err := service.git.InspectDiscard(
		ctx,
		worktree.Specification{
			Project:    lease.Project,
			Path:       lease.Path,
			Branch:     lease.Branch,
			BaseCommit: lease.BaseCommit,
		},
	)
	if err != nil {
		return err
	}
	if inventory.RequiresDecision() {
		return &DiscardDecisionRequired{Inventory: inventory}
	}
	return nil
}

func (service *Service) checkDiscardReports(messageRoot, taskID string) error {
	recipient, err := inbox.ParseRecipient(taskID)
	if err != nil {
		return err
	}
	reports, err := inbox.New(filepath.Join(service.root, "reports"))
	if err != nil {
		return err
	}
	saved, err := reports.List(recipient)
	if err != nil {
		return err
	}
	// Any saved task report deserves review, including reports from an earlier restart.
	if len(saved) != 0 {
		return errors.New("discard refused: saved reports require operator review")
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return err
	}
	pending, err := messages.List(service.primaryRecipient)
	if err != nil {
		return err
	}
	for _, message := range pending {
		reply, err := taskprotocol.DecodeReply(message.Body, message.Key, service.root)
		if err == nil && reply.Task == taskID {
			return errors.New("discard refused: pending worker reply requires operator review")
		}
	}
	return nil
}
