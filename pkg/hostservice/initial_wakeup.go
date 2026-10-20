package hostservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/taskstate"
)

var errInitialWakeupStopped = errors.New("initial wakeup registration is no longer pending")

func (registry *controllerRegistry) awaitInitialWakeup(key controllerKey) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed || registry.ctx.Err() != nil {
		return context.Canceled
	}
	if entry := registry.entries[key]; entry != nil && entry.active {
		return nil
	}
	registry.awaitingInitialWakeup[key] = struct{}{}
	return nil
}

func (registry *controllerRegistry) initialWakeupTasks() []controllerKey {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	keys := make([]controllerKey, 0, len(registry.awaitingInitialWakeup))
	for key := range registry.awaitingInitialWakeup {
		keys = append(keys, key)
	}
	return keys
}

func (registry *controllerRegistry) forgetInitialWakeup(key controllerKey) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.awaitingInitialWakeup, key)
}

func initialWakeupEligible(snapshot taskstate.Snapshot) bool {
	return snapshot.State == taskstate.InFlight && snapshot.Worker != nil &&
		!snapshot.Worker.PendingAdoption &&
		snapshot.Completion == nil
}

func (server *Server) reconcileInitialTasks(ctx context.Context) {
	if server.config.Recovery == RecoveryMaintenance {
		return
	}
	for _, key := range server.controllers.initialWakeupTasks() {
		if ctx.Err() != nil {
			return
		}
		attempt, cancel := context.WithTimeout(ctx, operationTimeout)
		err := server.reconcileInitialTask(attempt, key)
		cancel()
		if err != nil && !errors.Is(err, errControllerBusy) &&
			!errors.Is(err, errInitialWakeupStopped) &&
			ctx.Err() == nil {
			server.report(fmt.Errorf("restore task %s: %w", key.id, err))
		}
	}
}

func (server *Server) reconcileInitialTask(ctx context.Context, key controllerKey) error {
	activity, err := hostactivity.Begin(server.config.Root)
	if err != nil {
		return err
	}
	defer activity.Close()
	release, err := server.controllers.reserveRecoveredLaunch(key)
	if err != nil {
		return err
	}
	defer release()
	task, err := server.store.Read(ctx, key.id)
	if errors.Is(err, taskstate.ErrTaskNotFound) {
		server.controllers.forgetInitialWakeup(key)
		server.report(
			fmt.Errorf("restore task %s: retired recovery: task record no longer exists", key.id),
		)
		return nil
	}
	if err != nil {
		return err
	}
	snapshot := task.Snapshot()
	if intent := snapshot.LaunchIntent; intent != nil {
		if task.Definition().Hold != nil && snapshot.Worker == nil {
			return server.retireInitialLaunch(ctx, key, intent, "task is held")
		}
		originalWorktree := intent.Worktree
		if intent.LeaseHolder != "" {
			originalWorktree = ""
		}
		project := intent.Project
		if intent.Remote != (taskstate.RemoteLease{}) {
			project = intent.Remote.PrimaryProject
		}
		_, err := server.executeInitialLaunch(ctx, LaunchRequest{
			TaskID: key.id, Machine: intent.Machine, Harness: intent.Harness, Project: project, Worktree: originalWorktree,
			Branch: intent.Branch, BaseCommit: intent.BaseCommit, Workspace: intent.Workspace,
			AgentArgs: intent.AgentArgs, Body: intent.Body,
		}, recoveryLaunch)
		if err != nil {
			if errors.Is(err, herdr.ErrWorkspaceNotFound) && snapshot.Worker == nil {
				return server.retireInitialLaunch(ctx, key, intent, "workspace no longer exists")
			}
			return err
		}
	} else if !initialWakeupEligible(snapshot) {
		server.controllers.forgetInitialWakeup(key)
		return nil
	}
	release()
	_, err = server.supervisionWithMode(
		ctx,
		Supervise,
		SupervisionRequest{TaskID: key.id},
		initialWakeupStart,
	)
	return err
}

// Recovery already owns the task operation reservation. Verify absence under
// the durable task lock so an operator cannot change the launch during retirement.
func (server *Server) retireInitialLaunch(
	ctx context.Context,
	key controllerKey,
	intent *taskstate.LaunchIntent,
	reason string,
) error {
	_, err := server.store.WithTask(ctx, key.id, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if _, ok := current.LaunchAbandonment(intent.Generation); ok {
			return nil
		}
		if reason == "task is held" && current.Definition().Hold == nil {
			return taskstate.ErrInvalidTransition
		}
		if _, err := current.AbandonInitialLaunch(intent.Generation); err != nil {
			return err
		}
		captured := current.Snapshot().LaunchIntent
		client, err := herdr.NewClient(server.config.HerdrExecutable).
			ForEndpoint(captured.Machine, captured.Session)
		if err != nil {
			return err
		}
		resources := herdr.LaunchResources{Name: captured.Name, Label: captured.Label}
		if captured.Endpoint != nil {
			resources.Tab, resources.Pane = captured.Endpoint.Tab, captured.Endpoint.Pane
		}
		if err := client.CheckLaunchAbsent(ctx, resources); err != nil {
			return err
		}
		_, err = locked.Apply(func(task taskstate.Task) (taskstate.Task, error) {
			return task.AbandonInitialLaunch(intent.Generation)
		})
		return err
	})
	if err != nil {
		return err
	}
	server.controllers.forgetInitialWakeup(key)
	server.report(
		fmt.Errorf(
			"restore task %s: retired initial launch %s: %s",
			key.id,
			intent.Generation,
			reason,
		),
	)
	return nil
}

func (server *Server) runInitialReconciliation(ctx context.Context) {
	ticker := time.NewTicker(server.config.Supervision.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			server.reconcileInitialTasks(ctx)
		}
	}
}

func (registry *controllerRegistry) startInitialWakeup(
	ctx context.Context,
	key controllerKey,
	endpoint controllerEndpoint,
	specification string,
	prepare func(context.Context) (preparedController, error),
) (bool, error) {
	return registry.startWithMode(ctx, key, endpoint, specification, prepare, initialWakeupStart)
}
