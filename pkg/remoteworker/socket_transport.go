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
	"remote socket operation is unconfirmed; retain resources",
)

type SocketTransport struct{ Runner sshcommand.Runner }

func (transport SocketTransport) Prepare(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	owner SocketOwner,
) error {
	return transport.perform(ctx, profile, tools, owner, "prepare", "prepared")
}

func (transport SocketTransport) Probe(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	owner SocketOwner,
) error {
	return transport.perform(ctx, profile, tools, owner, "probe", "reachable")
}

func (transport SocketTransport) perform(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	owner SocketOwner,
	operation, status string,
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
		tools.Concierge, "worker-socket", operation, "--directory", tools.TaskDirectory,
		"--task", owner.Task, "--generation", owner.Generation, "--verifier", owner.Verifier)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, stderr, err := transport.Runner.Run(ctx, arguments, nil)
	if err == nil && ctx.Err() == nil && len(stderr) == 0 &&
		strings.TrimSpace(string(stdout)) == "status: "+status {
		return nil
	}
	for _, known := range []error{ErrSocketOwnership, ErrSocketActive, ErrTunnelUnreachable} {
		encoded, _ := json.Marshal(known.Error())
		if strings.Contains(string(stdout), "error: "+string(encoded)+"\n") {
			return known
		}
	}
	return ErrSocketOperationUnconfirmed
}

type SSHTunnelRemote struct{ Runner sshcommand.Runner }

func (remote SSHTunnelRemote) Prepare(ctx context.Context, spec TunnelSpec) error {
	return remote.perform(ctx, spec, "prepare", "prepared")
}

func (remote SSHTunnelRemote) Probe(ctx context.Context, spec TunnelSpec) error {
	return remote.perform(ctx, spec, "probe", "reachable")
}

func (remote SSHTunnelRemote) perform(
	ctx context.Context,
	spec TunnelSpec,
	operation, status string,
) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	profile := herdr.SavedMachine{ID: spec.MachineID, Target: spec.Target, Enabled: true}
	transport := SocketTransport{Runner: remote.Runner}
	if err := transport.perform(ctx, profile, spec.Tools, spec.Owner, operation, status); err != nil {
		return err
	}
	if spec.Authentication == sshcommand.ForwardedAgent {
		return transport.perform(ctx, profile, spec.Tools, spec.Owner, "agent-"+operation, status)
	}
	return nil
}
