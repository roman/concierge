package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrSocketOperationUnconfirmed = errors.New(
	"remote socket preparation is unconfirmed; retain resources",
)

type SocketTransport struct{ Runner sshcommand.Runner }

func (transport SocketTransport) Prepare(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	owner SocketOwner,
) error {
	if transport.Runner == nil || !profile.Enabled || profile.ID == "" {
		return errors.New(
			"remote socket transport requires an enabled saved machine and SSH runner",
		)
	}
	if err := tools.Validate(); err != nil {
		return err
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	arguments, err := sshcommand.TrustedBatchArguments(profile.Target,
		tools.Concierge, "worker-socket", "prepare", "--directory", tools.TaskDirectory,
		"--task", owner.Task, "--generation", owner.Generation, "--verifier", owner.Verifier)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, stderr, err := transport.Runner.Run(ctx, arguments, nil)
	if err == nil && ctx.Err() == nil && len(stderr) == 0 &&
		strings.TrimSpace(string(stdout)) == "status: prepared" {
		return nil
	}
	for _, known := range []error{ErrSocketOwnership, ErrSocketActive} {
		encoded, _ := json.Marshal(known.Error())
		if strings.Contains(string(stdout), "error: "+string(encoded)+"\n") {
			return known
		}
	}
	return ErrSocketOperationUnconfirmed
}
