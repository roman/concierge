package taskops

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskstate"
)

func (worker *SupervisedWorker) restartRemote(
	ctx context.Context,
	task *taskstate.LockedTask,
) error {
	snapshot := task.Current().Snapshot()
	bound := snapshot.Worker
	if worker.remoteRuntime == nil || worker.tunnelProbe == nil || bound == nil ||
		bound.Lease.Remote == (taskstate.RemoteLease{}) || bound.Lease.Remote.PrimaryRoot != worker.service.root || bound.PendingGeneration != "" {
		return ErrRemoteLaunchUnavailable
	}
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil ||
		snapshot.Definition.Hold != nil ||
		bound.PendingAdoption {
		return taskstate.ErrInvalidTransition
	}
	if err := validateNativeLaunch(bound.Harness, bound.AgentArgs); err != nil {
		return err
	}
	client, err := worker.boundClient(ctx, *bound)
	if err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.HerdrChannel, err)
	}
	_, err = client.Get(ctx, bound.Name)
	if err == nil {
		return herdr.ErrAgentPresent
	}
	if !errors.Is(err, herdr.ErrNotFound) {
		return worker.remoteRestartFailure(ctx, task, taskstate.HerdrChannel, err)
	}
	if err := worker.recordConnection(task, taskstate.HerdrChannel, nil); err != nil {
		return err
	}
	if remoteRestartNeedsTunnel(*bound) {
		if bound.Connections != nil && bound.Connections.Tunnel.Waiting(worker.now()) {
			return supervise.ErrUnreachable
		}
		probe, cancel := context.WithTimeout(ctx, remoteProbeTimeout)
		err := worker.tunnelProbe.Probe(probe, worker.taskID, bound.Generation)
		if err == nil {
			err = probe.Err()
		}
		cancel()
		if err := worker.remoteRestartFailure(ctx, task, taskstate.TunnelChannel, err); err != nil {
			return err
		}
	}
	generation := bound.PendingLaunch
	if generation == "" {
		generation = "s" + rand.Text()
	}
	tools, err := remoteworker.ToolsForGeneration(bound.Lease.Remote, worker.taskID, generation)
	if err != nil {
		return err
	}
	protocol, err := RemoteWorkerProtocol(worker.taskID, tools)
	if err != nil {
		return err
	}
	arguments, err := remoteNativeLaunchArguments(
		bound.Harness,
		bound.AgentArgs,
		protocol,
		remoteLaunchEnvironment{
			Task:       worker.taskID,
			Generation: generation,
			Directory:  tools.TaskDirectory,
		},
	)
	if err != nil {
		return err
	}
	if bound.LaunchAttemptGeneration == generation {
		return worker.remoteRestartFailure(
			ctx,
			task,
			taskstate.HerdrChannel,
			fmt.Errorf(
				"%w: remote start was submitted; inspect the native pane before another start",
				ErrNativeCheckpoint,
			),
		)
	}
	if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) { return current.ReserveLaunch(generation) }); err != nil {
		return err
	}
	if err := worker.checkPendingResolutions(*bound); err != nil {
		if errors.Is(err, errPendingAcceptance) {
			_, cancelErr := task.Apply(taskstate.Task.CancelLaunch)
			return errors.Join(err, cancelErr)
		}
		return err
	}
	if err := worker.reserveRestartRecovery(task, generation); err != nil {
		return err
	}
	site, err := worker.prepareRemoteRuntime(ctx, task, generation)
	if err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.TunnelChannel, err)
	}
	if err := worker.finishRestartRecovery(*task.Current().Snapshot().Worker); err != nil {
		return err
	}
	if err := worker.sendBootstrap(generation); err != nil {
		return err
	}
	if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) { return current.AdvanceGeneration(generation) }); err != nil {
		return err
	}
	if err := task.CheckCurrent(); err != nil {
		return err
	}
	if err := client.ReportPaneTitle(ctx, bound.Pane, task.Current().Definition().DisplayName()); err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.HerdrChannel, err)
	}
	if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) { return current.RequestRestartAgent(generation) }); err != nil {
		return err
	}
	if err := client.StartRemoteInWorktree(ctx, bound.Pane, bound.Name, bound.Harness, bound.Worktree, arguments, site.Site.Environment); err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.HerdrChannel, err)
	}
	if err := worker.confirmRemoteRestart(ctx, task, client); err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.HerdrChannel, err)
	}
	if err := worker.activateRemoteRuntime(ctx, task, site); err != nil {
		return worker.remoteRestartFailure(ctx, task, taskstate.TunnelChannel, err)
	}
	return worker.recordConnection(task, taskstate.TunnelChannel, nil)
}

func remoteRestartNeedsTunnel(bound taskstate.Worker) bool {
	if bound.PendingLaunch == "" {
		return true
	}
	if bound.PendingLaunch == bound.Generation {
		return false
	}
	return bound.Access == nil || !bound.Access.Revoked
}

func (worker *SupervisedWorker) confirmRemoteRestart(
	ctx context.Context,
	task *taskstate.LockedTask,
	client herdr.Client,
) error {
	bound := task.Current().Snapshot().Worker
	agent, err := client.Get(ctx, bound.Name)
	if err != nil {
		return err
	}
	if err := validateRemoteRestartAgent(*bound, agent); err != nil {
		return err
	}
	if err := worker.finishRestartRecovery(*bound); err != nil {
		return err
	}
	if _, err := task.Apply(taskstate.Task.CompleteLaunch); err != nil {
		return err
	}
	return worker.recordConnection(task, taskstate.HerdrChannel, nil)
}

func validateRemoteRestartAgent(bound taskstate.Worker, agent herdr.Agent) error {
	if agent.PaneID != bound.Pane || agent.Kind != bound.Harness {
		return ErrInitialLaunchIdentity
	}
	if agent.Status == herdr.Blocked {
		return ErrNativeCheckpoint
	}
	resume, err := nativeResumeArguments(bound.Harness, agent.Session)
	if err != nil {
		return err
	}
	if len(bound.AgentArgs) < len(resume) ||
		!slices.Equal(bound.AgentArgs[len(bound.AgentArgs)-len(resume):], resume) {
		return ErrInitialLaunchIdentity
	}
	return nil
}

func (worker *SupervisedWorker) remoteRestartFailure(
	ctx context.Context,
	task *taskstate.LockedTask,
	channel taskstate.ConnectionChannel,
	failure error,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(failure, taskstate.ErrPublicationUnconfirmed) {
		return failure
	}
	return worker.recordConnection(task, channel, failure)
}
