package taskops

import (
	"context"
	"fmt"
	"sort"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func (service *Service) initialLaunchArguments(
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) ([]string, error) {
	if intent.Machine == "" {
		protocol, err := service.workerProtocol(taskID, config.MessageRoot)
		if err != nil {
			return nil, err
		}
		return nativeLaunchArguments(intent.Harness, intent.AgentArgs, intent.Generation, protocol)
	}
	tools, err := remoteworker.ToolsForGeneration(intent.Remote, taskID, intent.Generation)
	if err != nil {
		return nil, err
	}
	protocol, err := RemoteWorkerProtocol(taskID, tools)
	if err != nil {
		return nil, err
	}
	return remoteNativeLaunchArguments(
		intent.Harness,
		intent.AgentArgs,
		protocol,
		remoteLaunchEnvironment{
			Task:       taskID,
			Generation: intent.Generation,
			Directory:  tools.TaskDirectory,
		},
	)
}

func (service *Service) initialPaneEnvironment(
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) ([]string, error) {
	if intent.Machine == "" {
		return []string{
			"CONCIERGE_WORKER=1",
			"CONCIERGE_HOME=" + service.root,
			"TINCAN_HOME=" + config.MessageRoot,
			"CONCIERGE_TASK_ID=" + taskID,
		}, nil
	}
	tools, err := remoteworker.ToolsForGeneration(intent.Remote, taskID, intent.Generation)
	if err != nil {
		return nil, err
	}
	variables, err := (remoteLaunchEnvironment{Task: taskID, Generation: intent.Generation, Directory: tools.TaskDirectory}).variables()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := []string{"CONCIERGE_WORKER=1", "CONCIERGE_TASK_ID=" + taskID}
	for _, key := range keys {
		environment = append(environment, key+"="+variables[key])
	}
	return environment, nil
}

func (service *Service) checkLaunchLease(
	ctx context.Context,
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) error {
	if intent.Machine == "" {
		return checkInitialLease(ctx, intent, config.Pool)
	}
	if err := service.validateRemoteInitialLaunch(intent, config); err != nil {
		return err
	}
	if intent.Lease == (taskstate.WorktreeLease{}) {
		return nil
	}
	lease, err := config.Remote.Worktrees.Acquire(
		ctx,
		config.Remote.Profile,
		remoteInitialWorktreeRequest(taskID, intent),
	)
	if err != nil {
		return err
	}
	if lease != intent.Lease {
		return ErrInitialLaunchIdentity
	}
	return nil
}

func (service *Service) prepareInitialWorktree(
	ctx context.Context,
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) error {
	if intent.Machine != "" {
		return service.checkLaunchLease(ctx, taskID, intent, config)
	}
	if intent.Lease != (taskstate.WorktreeLease{}) {
		if err := checkInitialLease(ctx, intent, config.Pool); err != nil {
			return err
		}
		return config.Git.PrepareLeased(ctx, launchWorktree(intent))
	}
	return config.Git.Prepare(ctx, launchWorktree(intent))
}

func (service *Service) verifyInitialWorktree(
	ctx context.Context,
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) error {
	if intent.Machine != "" {
		return service.checkLaunchLease(ctx, taskID, intent, config)
	}
	return config.Git.VerifyIdentity(ctx, launchWorktree(intent))
}

func (service *Service) activateInitialRemote(
	ctx context.Context,
	taskID string,
	intent *taskstate.LaunchIntent,
	config InitialLaunchConfig,
) error {
	if intent.Machine == "" {
		return nil
	}
	tools, err := remoteworker.ToolsForGeneration(intent.Remote, taskID, intent.Generation)
	if err != nil {
		return err
	}
	if err := config.Remote.Activate(ctx, taskID, intent.Generation, tools); err != nil {
		return err
	}
	task, err := service.store.Read(ctx, taskID)
	if err != nil {
		return err
	}
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if task.State() != taskstate.InFlight || snapshot.Completion != nil ||
		task.Definition().Hold != nil ||
		worker == nil ||
		worker.Machine != intent.Machine ||
		worker.Generation != intent.Generation ||
		worker.PendingAdoption ||
		worker.PendingGeneration != "" ||
		worker.PendingLaunch != "" {
		return taskstate.ErrInvalidTransition
	}
	access := worker.Access
	if access == nil || access.Revoked || access.Task != taskID ||
		access.Generation != intent.Generation ||
		access.Validate() != nil {
		return fmt.Errorf("%w: active generation grant is missing", ErrRemoteLaunchUnavailable)
	}
	return nil
}
