package hostservice

import (
	"context"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteLaunchResponsesMatchPrimaryProjectAndSavedMachine(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	task, err := fixture.server.store.Read(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	intent := task.Snapshot().LaunchIntent
	input := LaunchRequest{
		TaskID: task.ID(), Machine: intent.Machine, Harness: intent.Harness,
		Project: intent.Remote.PrimaryProject, Branch: intent.Branch,
		BaseCommit: intent.BaseCommit, Workspace: intent.Workspace, Body: intent.Body,
	}
	if err := validateLaunchInput(&input); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"pending", "completed"} {
		t.Run(stage, func(t *testing.T) {
			selected := task
			response := Response{Outcome: Pending, Error: "activation pending"}
			if stage == "completed" {
				var err error
				selected, err = selected.CompleteInitialLaunch(intent.Generation)
				if err != nil {
					t.Fatal(err)
				}
				response = Response{Outcome: Completed}
			}
			snapshot := selected.Snapshot()
			response.Transition = &snapshot
			if err := validateResponse(response, Request{Operation: Launch, Launch: &input}); err != nil {
				t.Fatal(err)
			}
			for _, mismatch := range []string{"machine", "local request", "primary project", "remote project as primary", "repository"} {
				t.Run(mismatch, func(t *testing.T) {
					request := input
					modified := selected.Snapshot()
					switch mismatch {
					case "machine":
						request.Machine = "different-machine"
					case "local request":
						request.Machine = ""
					case "primary project":
						request.Project = "/different"
					case "remote project as primary":
						request.Project = intent.Project
					case "repository":
						modified.Definition.Repo = "different"
					}
					wrong := response
					wrong.Transition = &modified
					if err := validateResponse(wrong, Request{Operation: Launch, Launch: &request}); err == nil {
						t.Fatal("accepted mismatched remote launch response")
					}
				})
			}
		})
	}
}

func TestLocalLaunchResponseRejectsRemoteWorkerWithSameProject(t *testing.T) {
	server, input := launchRequestFixture(t)
	input.TaskID, input.Worktree = "task-1", "/worktree"
	task, err := server.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := task.Snapshot()
	snapshot.Worker.Machine = "different-machine"
	if _, err := taskstate.NewMigrated(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := validateLaunchResponse(Response{Transition: &snapshot}, Request{Operation: Launch, Launch: &input}); err == nil {
		t.Fatal("accepted remote worker for a local request")
	}
}
