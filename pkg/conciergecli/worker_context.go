package conciergecli

import (
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/replyoutbox"
)

type workerContextCommands struct {
	Install struct {
		Directory        string `required:"" help:"Private remote directory containing the task secret."`
		StateRoot        string `required:"" help:"Primary state root metadata for original reply identity; never opened locally."`
		Task             string `required:"" help:"Task recorded by the primary."`
		Generation       string `required:"" help:"Generation recorded by the primary."`
		PrimaryRecipient string `required:"" help:"Primary recipient metadata for original reply identity."`
		Verifier         string `required:"" help:"SHA-256 verifier of the existing task secret."`
	} `cmd:"" help:"Bind offline reply metadata without replacing another launch context."`
}

func (command *workerContextCommands) run(output io.Writer) int {
	input := command.Install
	if err := replyoutbox.InstallContext(input.Directory, replyoutbox.Context{
		Format: 1, Root: input.StateRoot, Task: input.Task, Generation: input.Generation,
		Recipient: input.PrimaryRecipient, Verifier: input.Verifier,
	}); err != nil {
		return fail(
			output,
			1,
			err,
			"retain the original task secret and reply context; retry identical setup from the primary",
		)
	}
	if _, err := fmt.Fprintln(output, "status: bound"); err != nil {
		return 1
	}
	return 0
}
