package conciergecli

import (
	"context"
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/remoteworker"
)

type workerSocketCommands struct {
	Prepare struct {
		Directory  string `required:"" help:"Absolute private remote task directory."`
		Task       string `required:"" help:"Task recorded by the primary."`
		Generation string `required:"" help:"Generation recorded by the primary."`
		Verifier   string `required:"" help:"SHA-256 verifier of the task secret."`
	} `cmd:"" help:"Remove an owned stale socket only after excluding an active listener."`
}

func (command *workerSocketCommands) run(ctx context.Context, output io.Writer) int {
	input := command.Prepare
	owner := remoteworker.SocketOwner{
		Task: input.Task, Generation: input.Generation, Verifier: input.Verifier,
	}
	if err := remoteworker.PrepareSocket(ctx, input.Directory, owner); err != nil {
		return fail(
			output,
			1,
			err,
			"retain the tunnel and task directory; inspect ownership before retrying",
		)
	}
	if _, err := fmt.Fprintln(output, "status: prepared"); err != nil {
		return 1
	}
	return 0
}
