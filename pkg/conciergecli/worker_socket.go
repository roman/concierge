package conciergecli

import (
	"context"
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/remoteworker"
)

type workerSocketCommands struct {
	Prepare workerSocketInput `cmd:"" help:"Remove an owned stale socket only after excluding an active listener."`
	Probe   workerSocketInput `cmd:"" help:"Check an owned forward with a bounded worker protocol round trip."`
}

type workerSocketInput struct {
	Directory  string `required:"" help:"Absolute private remote task directory."`
	Task       string `required:"" help:"Task recorded by the primary."`
	Generation string `required:"" help:"Generation recorded by the primary."`
	Verifier   string `required:"" help:"SHA-256 verifier of the task secret."`
}

func (command *workerSocketCommands) run(
	ctx context.Context,
	selected string,
	output io.Writer,
) int {
	input := command.Prepare
	operation, status := remoteworker.PrepareSocket, "prepared"
	if selected == "worker-socket probe" {
		input, operation, status = command.Probe, remoteworker.ProbeSocket, "reachable"
	}
	owner := remoteworker.SocketOwner{
		Task: input.Task, Generation: input.Generation, Verifier: input.Verifier,
	}
	if err := operation(ctx, input.Directory, owner); err != nil {
		return fail(
			output,
			1,
			err,
			"retain the tunnel and task directory; inspect ownership before retrying",
		)
	}
	if _, err := fmt.Fprintf(output, "status: %s\n", status); err != nil {
		return 1
	}
	return 0
}
