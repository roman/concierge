package conciergecli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/sshcommand"
)

type machineLabelCommand struct {
	Label string `arg:"" help:"Saved Herdr machine label or ID."`
}

func (machineLabelCommand) Help() string {
	return `Declare the remote base and repositories in <root>/config/machines.json.
Use the ID from herdr machine list --json as the key. Pin the saved SSH target.
Example (replace the ID, paths, and repository URL):

{"SAVED_MACHINE_ID":{"target":"baker","base":"/Users/roman/concierge","agent":"codex","repositories":[{"name":"concierge","url":"git@github.com:roman/concierge.git"}]}}

The agent must be codex or claude. Include every dependency and project repository.
The concierge entry supplies the client source. Configuration changes create no credentials.

Examples:
  concierge machine check baker
  concierge machine status baker`
}

type machineCommands struct {
	Herdr   string                `default:"herdr" help:"Executable that owns the saved machine catalog."`
	Check   machineLabelCommand   `cmd:"" help:"Check SSH trust, platform, tools, repository access, and agent login without changes."`
	Status  machineLabelCommand   `cmd:"" help:"Read fresh prerequisites and confirm the built client on a configured saved machine."`
	Prepare machinePrepareCommand `cmd:"" help:"Prepare private repositories and build the committed client through the primary host."`
}

func (commands machineCommands) run(
	ctx context.Context,
	root, selected string,
	output io.Writer,
) int {
	catalog := herdr.MachineCatalog{Runner: herdr.NewClient(commands.Herdr).Runner}
	if selected == "machine status <label>" {
		return runMachineStatus(
			ctx,
			root,
			commands.Status.Label,
			catalog,
			sshcommand.SystemRunner{},
			output,
		)
	}
	return runMachineCheck(
		ctx,
		root,
		commands.Check.Label,
		catalog,
		sshcommand.SystemRunner{},
		output,
	)
}

type machineInspection int

const (
	inspectPrerequisites machineInspection = iota
	inspectPreparedClient
)

func runMachineCheck(
	ctx context.Context,
	root, label string,
	machines herdr.MachineResolver,
	runner sshcommand.Runner,
	output io.Writer,
) int {
	return inspectMachine(ctx, root, label, machines, runner, inspectPrerequisites, output)
}

func runMachineStatus(
	ctx context.Context,
	root, label string,
	machines herdr.MachineResolver,
	runner sshcommand.Runner,
	output io.Writer,
) int {
	return inspectMachine(ctx, root, label, machines, runner, inspectPreparedClient, output)
}

func inspectMachine(
	ctx context.Context,
	root, label string,
	machines herdr.MachineResolver,
	runner sshcommand.Runner,
	inspection machineInspection,
	output io.Writer,
) int {
	machine, err := machines.Resolve(ctx, label)
	if err != nil {
		return fail(output, 1, err, "register an enabled machine with herdr machine add and retry")
	}
	configurationPath := filepath.Join(root, "config", "machines.json")
	data, err := readOptionalConfiguration(configurationPath)
	var configuration machineprep.Configuration
	if err == nil {
		configuration, err = machineprep.DecodeConfiguration(data, machine)
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"configure "+configurationPath+" with the saved machine ID, target, base, agent, and repositories; run concierge machine check --help",
		)
	}
	report, err := (machineprep.Checker{Runner: runner}).Check(
		ctx,
		machine,
		configuration.Agent,
		configuration.Repositories,
	)
	if err != nil {
		return fail(output, 1, err, "correct "+configurationPath+" and retry")
	}
	status := "missing"
	var client machineprep.Client
	if inspection == inspectPreparedClient {
		clientCheck := machineprep.Check{
			Name:   "client",
			Status: "unconfirmed",
			Step: "Restore SSH access and run concierge machine prepare " + quote(
				label,
			) + " through the primary.",
		}
		if len(report.Checks) > 0 && report.Checks[0].Status == "ready" {
			client, err = (machineprep.Preparer{Runner: runner}).ReadClient(
				ctx,
				machine,
				configuration,
			)
			if err == nil {
				clientCheck.Status, clientCheck.Step = "ready", ""
			}
		}
		report.Checks = append(report.Checks, clientCheck)
		if clientCheck.Status == "unconfirmed" {
			status = "unconfirmed"
		}
	}
	if report.Ready() {
		status = "ready"
	}
	if _, err = fmt.Fprintf(output, "machine: %s\nmachine_id: %s\nbase: %s\nstatus: %s\nchecks: %d\n", quote(label), quote(machine.ID), quote(configuration.Base), status, len(report.Checks)); err != nil {
		return 1
	}
	if client.Commit != "" {
		if _, err := fmt.Fprintf(output, "client_commit: %s\nclient: %s\n", quote(client.Commit), quote(client.Executable)); err != nil {
			return 1
		}
	}
	for _, check := range report.Checks {
		if _, err = fmt.Fprintf(output, "check: %s\n  status: %s\n", quote(check.Name), check.Status); err != nil {
			return 1
		}
		if check.Detail != "" {
			if _, err = fmt.Fprintf(output, "  detail: %s\n", quote(check.Detail)); err != nil {
				return 1
			}
		}
		if check.Step != "" {
			if _, err = fmt.Fprintf(output, "  help: %s\n", quote(check.Step)); err != nil {
				return 1
			}
		}
	}
	if !report.Ready() {
		return 1
	}
	return 0
}
