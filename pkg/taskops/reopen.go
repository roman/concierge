package taskops

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
)

// Reopen returns an accepted scout to active work with its recorded binding.
func (service *Service) Reopen(
	ctx context.Context,
	taskID string,
	client herdr.Client,
) (taskstate.Task, error) {
	var replacement *herdr.PaneDetails
	updated, err := service.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if err := current.CheckReopen(); err != nil {
			return err
		}
		bound := current.Snapshot().Worker
		client, err := client.ForEndpoint(bound.Machine, bound.Session)
		if err != nil {
			return err
		}
		agent, agentErr := client.Get(ctx, bound.Name)
		if agentErr == nil && (agent.PaneID != bound.Pane || agent.Kind != bound.Harness) {
			return errors.New("recorded worker identity differs from the live agent")
		}
		if agentErr != nil && !errors.Is(agentErr, herdr.ErrNotFound) {
			return agentErr
		}
		if agentErr != nil {
			if err := checkRecordedResume(*bound); err != nil {
				return err
			}
		}
		target, err := herdr.ParseEndpoint(bound.Machine, bound.Session, bound.Pane)
		if err != nil {
			return err
		}
		presence := client.ConfirmPaneGone(ctx, target)
		if presence != nil && !errors.Is(presence, herdr.ErrPanePresent) {
			return presence
		}
		if presence == nil && agentErr == nil {
			return errors.New("agent registration remains for the missing pane")
		}
		if agentErr == nil && client.Health(ctx, target).Endpoint != herdr.Alive {
			return errors.New(
				"worker liveness is unconfirmed; inspect the recorded agent and pane before reopening",
			)
		}
		if presence != nil {
			directory, err := client.ForegroundDirectory(ctx, bound.Pane)
			if err != nil {
				return err
			}
			if err := verifyEndpointWorktree(bound.Machine, bound.Worktree, directory); err != nil {
				return err
			}
		}
		if _, err := locked.Apply(func(task taskstate.Task) (taskstate.Task, error) { return task.Reopen(time.Now()) }); err != nil {
			return err
		}
		if presence != nil {
			return nil
		}
		details, err := service.recoverReopenPane(ctx, locked, client)
		if err == nil {
			replacement = &details
		}
		return err
	})
	if err != nil && !errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		return updated, err
	}
	if err == nil && replacement != nil {
		bound := updated.Snapshot().Worker
		updated, err = service.store.RelocateReopenedWorker(
			ctx,
			taskID,
			bound.Generation,
			bound.Pane,
			taskstate.LaunchEndpoint{
				Workspace: replacement.WorkspaceID,
				Tab:       replacement.TabID,
				Pane:      replacement.PaneID,
			},
		)
	}
	return updated, errors.Join(err, taskview.Rebuild(ctx, service.root))
}

func checkRecordedResume(bound taskstate.Worker) error {
	arguments := bound.AgentArgs
	if len(arguments) < 2 {
		return errors.New("recorded native session identity is missing; cannot resume")
	}
	identity := arguments[len(arguments)-1]
	reference := &herdr.SessionReference{
		Source: "herdr:" + bound.Harness,
		Agent:  bound.Harness,
		Kind:   "id",
		Value:  identity,
	}
	resume, err := nativeResumeArguments(bound.Harness, reference)
	if err != nil || !slices.Equal(arguments[len(arguments)-2:], resume) {
		return errors.New("recorded native session identity is missing or invalid; cannot resume")
	}
	return validateNativeLaunch(bound.Harness, arguments)
}

func (service *Service) recoverReopenPane(
	ctx context.Context,
	locked *taskstate.LockedTask,
	client herdr.Client,
) (herdr.PaneDetails, error) {
	snapshot := locked.Current().Snapshot()
	bound := snapshot.Worker
	identity := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%d",
		service.root,
		locked.Current().ID(),
		bound.Generation,
		len(snapshot.Reopens),
	)
	digest := sha256.Sum256([]byte(identity))
	label := fmt.Sprintf("concierge-reopen-%x", digest[:16])
	var details herdr.PaneDetails
	var err error
	if snapshot.Reopens[len(snapshot.Reopens)-1].PaneRequested {
		details, err = client.FindWorkspace(ctx, label)
		if err != nil {
			return details, fmt.Errorf(
				"reopen pane creation remains unresolved; inspect workspace %s: %w",
				label,
				err,
			)
		}
	} else {
		if _, err := locked.Apply(taskstate.Task.RequestReopenPane); err != nil {
			return details, err
		}
		details, err = client.CreateWorkspace(ctx, herdr.WorkspaceInput{Directory: bound.Worktree, Label: label})
		if err != nil {
			return details, err
		}
	}
	if err := verifyEndpointWorktree(bound.Machine, bound.Worktree, details.ForegroundDirectory); err != nil {
		return details, err
	}
	return details, nil
}
