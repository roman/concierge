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
	Herdr  string              `default:"herdr" help:"Executable that owns the saved machine catalog."`
	Check  machineLabelCommand `cmd:"" help:"Check SSH trust, platform, tools, repository access, and agent login without changes."`
	Status machineLabelCommand `cmd:"" help:"Read fresh prerequisite status for a configured saved machine."`
}

func (commands machineCommands) run(
	ctx context.Context,
	root, selected string,
	output io.Writer,
) int {
	label := commands.Check.Label
	if selected == "machine status <label>" {
		label = commands.Status.Label
	}
	catalog := herdr.MachineCatalog{Runner: herdr.NewClient(commands.Herdr).Runner}
	return runMachineCheck(ctx, root, label, catalog, sshcommand.SystemRunner{}, output)
}

func runMachineCheck(
	ctx context.Context,
	root, label string,
	machines herdr.MachineResolver,
	runner sshcommand.Runner,
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
	if report.Ready() {
		status = "ready"
	}
	if _, err = fmt.Fprintf(output, "machine: %s\nmachine_id: %s\nbase: %s\nstatus: %s\nchecks: %d\n", quote(label), quote(machine.ID), quote(configuration.Base), status, len(report.Checks)); err != nil {
		return 1
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
