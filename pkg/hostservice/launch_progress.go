package hostservice

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

// Allow the state-version lock wait and leave response time within the I/O grace.
const launchInspectionTimeout = 3 * time.Second

// trackedLaunchResult reports matching recorded work without claiming completion.
func (server *Server) trackedLaunchResult(
	ctx context.Context,
	input LaunchRequest,
	cause error,
) (Response, error) {
	if errors.Is(cause, ErrLaunchAlreadyBound) || errors.Is(cause, ErrLaunchRetired) {
		return Response{}, cause
	}
	if err := validateLaunchInput(&input); err != nil {
		return Response{}, cause
	}
	key := controllerKey{kind: taskController, id: input.TaskID}
	if !slices.Contains(server.controllers.initialWakeupTasks(), key) {
		return Response{}, cause
	}
	inspection, cancel := context.WithTimeout(context.WithoutCancel(ctx), launchInspectionTimeout)
	defer cancel()
	task, err := server.store.Read(inspection, input.TaskID)
	if err != nil {
		return Response{}, errors.Join(ErrOutcomeUnknown, cause, err)
	}
	snapshot := task.Snapshot()
	response := Response{Outcome: Pending, Transition: &snapshot, Error: cause.Error()}
	if err := validateLaunchProgress(response, Request{Operation: Launch, Launch: &input}); err != nil {
		return Response{}, cause
	}
	if intent := snapshot.LaunchIntent; intent != nil {
		if snapshot.Definition.Hold != nil || errors.Is(cause, herdr.ErrWorkspaceNotFound) ||
			errors.Is(cause, herdr.ErrResponse) || errors.Is(cause, herdr.ErrAgentPresent) ||
			errors.Is(cause, taskops.ErrInitialLaunchIdentity) ||
			errors.Is(cause, taskops.ErrNativeCheckpoint) ||
			errors.Is(cause, worktree.ErrIdentity) ||
			errors.Is(cause, taskstate.ErrInvalidTransition) ||
			(intent.Endpoint == nil && intent.Phase == taskstate.LaunchPaneRequested && errors.Is(cause, herdr.ErrNotFound)) {
			response.Outcome = Blocked
		}
		if intent.Endpoint != nil && response.Outcome == Pending {
			client, err := herdr.NewClient(server.config.HerdrExecutable).
				ForEndpoint(intent.Machine, intent.Session)
			if err == nil {
				agent, err := client.Get(inspection, intent.Name)
				if err == nil &&
					(agent.PaneID != intent.Endpoint.Pane || agent.Kind != intent.Harness || agent.Status == herdr.Blocked) {
					response.Outcome = Blocked
				}
			}
		}
	}
	return response, nil
}

func validateLaunchProgress(response Response, request Request) error {
	if request.Operation != Launch || response.Transition == nil || response.Error == "" ||
		response.Task != nil || response.Abandoned != nil || response.Status != nil || response.Supervision != nil || response.Control != nil || response.Assignment != nil || response.Stopped != nil || response.Resolution != nil {
		return errors.New("host returned incomplete launch progress")
	}
	task, err := taskstate.NewMigrated(*response.Transition)
	if err != nil {
		return err
	}
	if task.ID() != request.Launch.TaskID {
		return errors.New("host returned another launch")
	}
	intent := task.Snapshot().LaunchIntent
	if err := validateNativeCheckpoint(response); err != nil {
		return err
	}
	if intent == nil {
		completed := response
		completed.Error = ""
		return validateLaunchResponse(completed, request)
	}
	candidate := request.Launch.intent(intent.Session, intent.Generation)
	if !request.Launch.matchesLocation(
		intent.Machine,
		intent.Project,
		intent.Remote,
		task.Definition().Repo,
	) {
		return errors.New("host returned a different launch destination")
	}
	candidate.Project, candidate.Remote = intent.Project, intent.Remote
	candidate.Name, candidate.Label = intent.Name, intent.Label
	if _, err := task.ReserveInitialLaunch(candidate); err != nil {
		return errors.New("host returned mismatched launch progress")
	}
	return nil
}

func (input LaunchRequest) matchesLocation(
	machine, project string,
	remote taskstate.RemoteLease,
	repository string,
) bool {
	if input.Machine != machine {
		return false
	}
	if machine == "" {
		return remote == (taskstate.RemoteLease{}) && input.Project == project
	}
	return remote != (taskstate.RemoteLease{}) && remote.Validate() == nil &&
		remote.Machine == machine && remote.Repository == repository &&
		remote.PrimaryProject == input.Project && remote.ProjectPath() == project
}

// launchRegistrationFailure retains the confirmed launch when registration ends.
func (server *Server) launchRegistrationFailure(
	input LaunchRequest,
	completed Response,
	cause error,
) (Response, error) {
	if err := validateLaunchResponse(completed, Request{Operation: Launch, Launch: &input}); err != nil {
		return Response{}, err
	}
	worker := completed.Transition.Worker
	state := server.controllers.launchRegistrationStatus(
		controllerKey{kind: taskController, id: input.TaskID},
		controllerEndpoint{machine: worker.Machine, session: worker.Session, pane: worker.Pane},
		supervisionSpecification(
			SupervisionRequest{TaskID: input.TaskID},
			server.config.Supervision,
		),
	)
	switch state {
	case registrationActive:
		return completed, nil
	case registrationPending:
		completed.Outcome = Pending
	case registrationStopped:
		completed.Outcome = Blocked
	}
	completed.Error = cause.Error()
	return completed, nil
}

type launchRegistrationState uint8

const (
	registrationActive launchRegistrationState = iota
	registrationPending
	registrationStopped
)

func (registry *controllerRegistry) launchRegistrationStatus(
	key controllerKey,
	endpoint controllerEndpoint,
	specification string,
) launchRegistrationState {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.entries[key]
	if entry != nil && entry.active && !entry.stopping && !entry.exited &&
		entry.endpoint == endpoint && entry.specification == specification {
		return registrationActive
	}
	if _, pending := registry.awaitingInitialWakeup[key]; pending {
		return registrationPending
	}
	return registrationStopped
}
