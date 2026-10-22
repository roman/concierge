package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type launchCommand struct {
	TaskID                string   `arg:"" help:"Queued task ID."`
	Machine               string   `help:"Saved Herdr machine label or ID; omit for a local worker."`
	Herdr                 string   `default:"herdr" env:"TINCAN_HERDR" help:"Executable that owns the saved machine catalog."`
	Harness               string   `required:"" enum:"codex,claude" help:"Worker harness."`
	Project               string   `required:"" help:"Absolute source repository path."`
	Worktree              string   `help:"Existing launch worktree path; omit for a treehouse lease."`
	Branch                string   `required:"" help:"New literal worktree branch."`
	BaseCommit            string   `help:"Full source commit ID; omit to fetch the project's target branch or remote default."`
	Workspace             string   `help:"Existing shared Herdr workspace ID; omit to create a task-owned workspace. Labels are unsupported."`
	AgentArgs             []string `name:"agent-arg" sep:"none" help:"Worker launch argument; repeat in order, using --agent-arg=VALUE for options. Remote Codex requires --agent-arg=--sandbox=workspace-write explicitly."`
	CodexApprovalReviewer string   `default:"native" enum:"native,auto-review" help:"Remote Codex approval reviewer. Native preserves current settings. Auto-review explicitly selects automatic review."`
	BodyFile              string   `required:"" help:"Assignment file, or - for stdin."`
}

func (command launchCommand) Validate() error {
	if command.Machine == "" {
		return nil
	}
	return hostservice.ValidateRemotePermissions(hostservice.LaunchRequest{
		Harness: command.Harness, AgentArgs: command.AgentArgs,
	})
}

func (command *launchCommand) run(
	ctx context.Context,
	root string,
	expectations hostservice.Expectations,
	stdin io.Reader,
	output io.Writer,
) int {
	agentArguments, err := command.agentArguments()
	if err != nil {
		return fail(
			output,
			2,
			err,
			"use --codex-approval-reviewer=auto-review only with --machine and --harness=codex. Native preserves the configured reviewer",
		)
	}
	body, err := readBodyFile(command.BodyFile, stdin)
	if err != nil {
		return fail(output, 1, err, "provide a readable --body-file or use --body-file - for stdin")
	}
	if err := taskops.ValidateInitialLaunch(command.Harness, agentArguments, body); err != nil {
		return fail(output, 2, err, "provide a nonblank assignment and compatible worker arguments")
	}
	body, err = appendBriefInclude(root, body)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"ask the operator to correct config/brief-include.md; no launch was requested",
		)
	}
	if command.BaseCommit == "" {
		command.BaseCommit, err = command.defaultBaseCommit(ctx, root)
		if err != nil {
			return fail(
				output,
				2,
				err,
				"inspect the project target branch or supply --base-commit with a full commit ID",
			)
		}
	}
	machineID := ""
	if command.Machine != "" {
		profile, err := (herdr.MachineCatalog{Runner: herdr.NewClient(command.Herdr).Runner}).Resolve(
			ctx,
			command.Machine,
		)
		if err != nil {
			return fail(
				output,
				2,
				err,
				"select an enabled saved machine from herdr machine list --json",
			)
		}
		machineID = profile.ID
	}
	response, err := hostservice.Call(ctx, filepath.Join(root, "host.sock"), hostservice.Request{
		Operation: hostservice.Launch, Expectations: &expectations,
		Launch: &hostservice.LaunchRequest{
			TaskID: command.TaskID, Machine: machineID, Harness: command.Harness, Project: command.Project,
			Worktree: command.Worktree, Branch: command.Branch, BaseCommit: command.BaseCommit,
			Workspace: command.Workspace, AgentArgs: agentArguments, Body: body,
		},
	})
	if response.Versions != nil {
		if writeErr := response.Versions.Write(output); writeErr != nil {
			return 1
		}
	}
	if err == nil &&
		(response.Outcome == hostservice.Pending || response.Outcome == hostservice.Blocked) {
		return writeLaunchProgress(output, command.TaskID, response)
	}
	if err == nil && response.Outcome != hostservice.Completed {
		err = errors.New(response.Error)
	}
	if err != nil {
		code := 1
		if response.Outcome == hostservice.Invalid {
			code = 2
		}
		return fail(
			output,
			code,
			err,
			"inspect concierge backlog show TASK_ID and worker show TASK_ID before another launch; an unknown outcome can include an active worker",
		)
	}
	worker := response.Transition.Worker
	_, err = fmt.Fprintf(
		output,
		"task: %s\nstate: %s\ngeneration: %s\nsession: %s\npane: %s\n",
		quote(command.TaskID),
		response.Transition.State,
		quote(worker.Generation),
		quote(worker.Session),
		quote(worker.Pane),
	)
	if err != nil {
		return 1
	}
	if worker.Machine != "" {
		if _, err := fmt.Fprintf(output, "machine: %s\n", quote(worker.Machine)); err != nil {
			return 1
		}
	}
	return 0
}

func (command launchCommand) defaultBaseCommit(ctx context.Context, root string) (string, error) {
	store, err := taskstate.NewStore(root)
	if err != nil {
		return "", err
	}
	task, err := store.Read(ctx, command.TaskID)
	if err != nil {
		return "", err
	}
	if intent := task.Snapshot().LaunchIntent; intent != nil {
		return intent.BaseCommit, nil
	}
	settings, err := readProjectSettings(
		filepath.Join(root, "config", "projects.json"),
		task.Definition().Repo,
	)
	if err != nil {
		return "", err
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	branch, err := (worktree.Client{Executable: git}).FetchBranch(
		ctx,
		command.Project,
		settings.Branch,
	)
	return branch.Tip, err
}

func (command launchCommand) agentArguments() ([]string, error) {
	arguments := slices.Clone(command.AgentArgs)
	switch command.CodexApprovalReviewer {
	case "", "native":
		return arguments, nil
	case "auto-review":
		if command.Machine == "" || command.Harness != "codex" {
			return nil, errors.New("automatic approval review requires a remote Codex launch")
		}
		return append(arguments, "-c", "approvals_reviewer=auto_review"), nil
	default:
		return nil, fmt.Errorf(
			"unknown Codex approval reviewer %q. Choose native or auto-review",
			command.CodexApprovalReviewer,
		)
	}
}

func appendBriefInclude(root, body string) (string, error) {
	include, err := readOptionalConfiguration(filepath.Join(root, "config", "brief-include.md"))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(include)) == "" {
		return body, nil
	}
	return body + "\n\n## Standing worker instructions\n\nTask-specific instructions take precedence over these defaults.\n\n" + string(
		include,
	), nil
}

func writeLaunchProgress(output io.Writer, taskID string, response hostservice.Response) int {
	snapshot := response.Transition
	phase := "awaiting_supervision"
	tracking := "automatic"
	if snapshot.LaunchIntent == nil && response.Outcome == hostservice.Blocked {
		phase = "supervision_stopped"
		tracking = "stopped"
	}
	generation := ""
	if snapshot.Worker != nil {
		generation = snapshot.Worker.Generation
	}
	if intent := snapshot.LaunchIntent; intent != nil {
		phase = string(intent.Phase)
		generation = intent.Generation
	}
	if _, err := fmt.Fprintf(output, "task: %s\nlaunch: %s\ntracking: %s\ngeneration: %s\nphase: %s\nreason: %s\nhelp: %s\n",
		quote(taskID), response.Outcome, tracking, quote(generation), phase, quote(response.Error), quote("inspect completion with concierge worker show "+quote(taskID))); err != nil {
		return 1
	}
	machine := ""
	if snapshot.Worker != nil {
		machine = snapshot.Worker.Machine
	}
	if snapshot.LaunchIntent != nil {
		machine = snapshot.LaunchIntent.Machine
	}
	if machine != "" {
		if _, err := fmt.Fprintf(output, "machine: %s\n", quote(machine)); err != nil {
			return 1
		}
	}
	if response.Checkpoint != nil {
		intent := snapshot.LaunchIntent
		if _, err := fmt.Fprintf(output, "checkpoint: %s\nnotification: %s\nsession: %s\npane: %s\nworktree: %s\ncheckpoint_help: %s\n", response.Checkpoint.Kind, response.Checkpoint.Notification, quote(intent.Session), quote(intent.Endpoint.Pane), quote(intent.Worktree), quote(response.Checkpoint.Help(intent))); err != nil {
			return 1
		}
	}
	if response.Outcome == hostservice.Blocked {
		if snapshot.LaunchIntent == nil {
			if _, err := fmt.Fprintf(output, "recovery: %s\n", quote("inspect the worker, then request supervision with concierge supervise task "+quote(taskID))); err != nil {
				return 1
			}
		}
		if response.Checkpoint == nil && snapshot.LaunchIntent != nil && snapshot.Worker == nil {
			if _, err := fmt.Fprintf(output, "recovery: %s\n", quote("inspect the native checkpoint and launch resources; safe abandonment requires concierge task abandon "+quote(taskID)+" --generation "+quote(generation))); err != nil {
				return 1
			}
		}
		return 1
	}
	return 0
}
