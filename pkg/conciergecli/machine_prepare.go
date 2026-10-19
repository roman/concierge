package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/roman/concierge/pkg/hostservice"
)

type machinePrepareCommand struct {
	Label        string `arg:"" help:"Saved Herdr machine label or ID."`
	ClientSource string `default:"." help:"Clean primary Concierge checkout; its committed HEAD supplies the remote client."`
}

func (machinePrepareCommand) Help() string {
	return machineLabelCommand{}.Help() + `

Preparation requires a format-three root and the current primary claim.
It checks prerequisites before remote changes. The remote builds the committed
client source with Nix. Its Git bundle contains no local Git configuration.
Commit source changes first. Supply repository keys and the agent login by hand.

Examples:
  concierge machine prepare dev-remote
  concierge machine prepare dev-remote --client-source /path/to/concierge`
}

func (command machinePrepareCommand) run(
	ctx context.Context,
	root string,
	expected hostservice.Expectations,
	output io.Writer,
) int {
	source, err := filepath.Abs(command.ClientSource)
	if err != nil {
		return fail(output, 2, err, "provide --client-source with the primary Concierge checkout")
	}
	response, err := hostservice.Call(ctx, filepath.Join(root, "host.sock"), hostservice.Request{
		Operation: hostservice.PrepareMachine, Expectations: &expected,
		MachinePreparation: &hostservice.MachinePreparationRequest{
			Label:        command.Label,
			ClientSource: source,
		},
	})
	if err == nil && response.Outcome != hostservice.Completed {
		err = errors.New(response.Error)
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"run concierge machine check "+quote(
				command.Label,
			)+"; correct its reported prerequisites, or supply a clean --client-source checkout with the declared origin, then retry",
		)
	}
	prepared := response.PreparedMachine
	if _, err := fmt.Fprintf(output, "machine: %s\nmachine_id: %s\nstatus: prepared\nclient_commit: %s\nclient: %s\n", quote(command.Label), quote(prepared.MachineID), quote(prepared.Client.Commit), quote(prepared.Client.Executable)); err != nil {
		return 1
	}
	return 0
}
