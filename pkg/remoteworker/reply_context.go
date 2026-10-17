package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrContextOperationUnconfirmed = errors.New(
	"remote reply context setup is unconfirmed; retain the launch identity",
)

type ContextTransport struct{ Runner sshcommand.Runner }

func (transport ContextTransport) Install(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	binding replyoutbox.Context,
) error {
	if transport.Runner == nil || !profile.Enabled || profile.ID == "" {
		return errors.New(
			"remote reply context setup requires an enabled saved machine and SSH runner",
		)
	}
	if err := tools.Validate(); err != nil {
		return err
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	arguments, err := sshcommand.TrustedBatchArguments(profile.Target,
		tools.Concierge, "worker-context", "install", "--directory", tools.TaskDirectory,
		"--state-root", binding.Root, "--task", binding.Task, "--generation", binding.Generation,
		"--primary-recipient", binding.Recipient, "--verifier", binding.Verifier)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, stderr, err := transport.Runner.Run(ctx, arguments, nil)
	if err == nil && ctx.Err() == nil && len(stderr) == 0 &&
		strings.TrimSpace(string(stdout)) == "status: bound" {
		return nil
	}
	encoded, _ := json.Marshal(replyoutbox.ErrContext.Error())
	if strings.Contains(string(stdout), "error: "+string(encoded)+"\n") {
		return replyoutbox.ErrContext
	}
	return ErrContextOperationUnconfirmed
}
