package hostservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func (server *Server) executeRemoteInitialLaunch(
	ctx context.Context,
	input LaunchRequest,
	admission launchAdmission,
) (Response, error) {
	if err := validateLaunchInput(&input); err != nil {
		return Response{}, err
	}
	if input.Worktree != "" {
		return Response{}, errors.New(
			"remote launches acquire an owned lease; omit the worktree path",
		)
	}
	if server.tunnels == nil {
		return Response{}, taskops.ErrRemoteLaunchUnavailable
	}
	existing, err := server.store.Read(ctx, input.TaskID)
	if err != nil {
		return Response{}, err
	}
	if err := completedLaunchConflict(existing.Snapshot()); err != nil {
		return Response{}, err
	}
	// Captured launches retain their original arguments during reconciliation.
	if existing.Snapshot().LaunchIntent == nil {
		if err := ValidateRemotePermissions(input); err != nil {
			return Response{}, err
		}
	}
	if err := remoteworker.ValidatePrimaryWorkerSocketPath(server.config.Root, input.TaskID); err != nil {
		return Response{}, err
	}
	profile, location, err := server.resolveRemoteLaunchLocation(
		ctx,
		input.Machine,
		input.Project,
		existing.Definition().Repo,
		existing.Snapshot().LaunchIntent,
	)
	if err != nil {
		return Response{}, err
	}
	intent := input.intent(profile.Session, "validation")
	intent.Project, intent.Remote = location.ProjectPath(), location
	intent.Label = existing.Definition().DisplayName()
	if current := existing.Snapshot().LaunchIntent; current != nil {
		intent.Generation, intent.Name, intent.Label = current.Generation, current.Name, current.Label
	}
	if _, err := existing.ReserveInitialLaunch(intent); err != nil {
		return Response{}, err
	}
	git := worktree.Client{Executable: server.config.GitExecutable}
	if err := git.Validate(ctx, worktree.Specification{
		Project: input.Project, Path: input.Project, Branch: input.Branch, BaseCommit: input.BaseCommit,
	}); err != nil {
		return Response{}, err
	}
	client := herdr.NewClient(server.config.HerdrExecutable)
	if existing.Snapshot().LaunchIntent == nil && input.Workspace != "" {
		bound, err := client.ForEndpoint(profile.ID, profile.Session)
		if err != nil {
			return Response{}, err
		}
		if err := bound.ValidateWorkspace(ctx, input.Workspace); err != nil {
			return Response{}, err
		}
	}
	if admission == operatorLaunch {
		if err := server.controllers.awaitInitialWakeup(controllerKey{kind: taskController, id: input.TaskID}); err != nil {
			return Response{}, err
		}
	}
	if existing.Snapshot().LaunchIntent == nil {
		intent.Generation = "s" + rand.Text()
		intent.Name = "c-" + strings.ToLower(intent.Generation)
	}
	if err := herdr.ValidateAgentName(intent.Name); err != nil {
		return Response{}, err
	}
	if _, err := server.reserveLaunchIntent(ctx, input.TaskID, intent); err != nil {
		return Response{}, err
	}
	runner := server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	var releaseEndpoint func()
	defer func() {
		if releaseEndpoint != nil {
			releaseEndpoint()
		}
	}()
	task, err := server.service.ContinueInitialLaunch(ctx, input.TaskID, intent.Generation,
		taskops.InitialLaunchConfig{
			Git: git, Herdr: client, MessageRoot: server.config.MessageRoot,
			Remote: &taskops.RemoteInitialLaunchConfig{
				Profile: profile, Worktrees: remoteworker.WorktreeTransport{Runner: runner},
				Activate: func(ctx context.Context, taskID, generation string, tools taskops.RemoteWorkerTools) error {
					return server.activateRemoteWorker(ctx, taskID, generation, profile, tools)
				},
			},
			ReserveEndpoint: func(endpoint taskstate.LaunchEndpoint) error {
				var err error
				releaseEndpoint, err = server.controllers.acquire(controllerEndpoint{
					machine: profile.ID, session: profile.Session, pane: endpoint.Pane,
				})
				return err
			},
		})
	if err != nil {
		if !errors.Is(err, taskops.ErrNativeCheckpoint) {
			return Response{}, err
		}
		checkpoint, noticeErr := server.launchNativeCheckpoint(ctx, input.TaskID)
		if checkpoint != nil {
			err = fmt.Errorf("native checkpoint (%s) requires operator review: %w",
				checkpoint.Kind, errors.Join(err, noticeErr))
		}
		return Response{Checkpoint: checkpoint}, err
	}
	snapshot := task.Snapshot()
	return Response{Transition: &snapshot}, nil
}

// ValidateRemotePermissions lets clients reject unsupported permissions before resource creation.
func ValidateRemotePermissions(input LaunchRequest) error {
	if input.Harness != "codex" {
		return nil
	}
	mode := ""
	for index := 0; index < len(input.AgentArgs); index++ {
		argument := input.AgentArgs[index]
		switch {
		case argument == "--sandbox" || argument == "-s":
			mode = ""
			index++
			if index < len(input.AgentArgs) {
				mode = input.AgentArgs[index]
			}
		case strings.HasPrefix(argument, "--sandbox="):
			mode = strings.TrimPrefix(argument, "--sandbox=")
		case strings.HasPrefix(argument, "-s="):
			mode = strings.TrimPrefix(argument, "-s=")
		}
	}
	if mode == "workspace-write" {
		return nil
	}
	return errors.New(
		"remote Codex needs its private task directory through --add-dir; select --agent-arg=--sandbox=workspace-write explicitly before launching; native default or read-only permissions cannot add writable roots",
	)
}
