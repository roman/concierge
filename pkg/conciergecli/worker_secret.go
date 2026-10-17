package conciergecli

import (
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/workeraccess"
)

type workerSecretCommands struct {
	Install struct {
		Directory string `required:"" help:"Absolute remote task directory; created with mode 0700 if absent."`
	} `cmd:"" help:"Read a secret from stdin and atomically install it without replacing another secret."`
	Remove struct {
		Directory string `required:"" help:"Absolute remote task directory."`
		Verifier  string `required:"" help:"Expected SHA-256 verifier from the revoked primary grant."`
	} `cmd:"" help:"Remove the matching secret after the primary revokes its grant."`
}

func (command *workerSecretCommands) run(selected string, stdin io.Reader, output io.Writer) int {
	var err error
	status := "installed"
	if selected == "worker-secret install" {
		err = workeraccess.InstallSecretFile(command.Install.Directory, stdin)
	} else {
		status = "removed"
		err = workeraccess.RemoveSecretFile(command.Remove.Directory, command.Remove.Verifier)
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"retain the task generation and verifier; inspect ownership and retry only the identical secret operation",
		)
	}
	if _, err := fmt.Fprintf(output, "status: %s\n", status); err != nil {
		return 1
	}
	return 0
}
