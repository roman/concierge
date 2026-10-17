package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// MachineRunner selects a saved remote machine without local session selection.
type MachineRunner interface {
	RunMachine(context.Context, string, ...string) ([]byte, []byte, error)
}

type machineCommandRunner struct{ executable string }

func (runner machineCommandRunner) RunMachine(
	ctx context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	command := exec.CommandContext(
		ctx,
		runner.executable,
		append([]string{"--machine", machine}, args...)...)
	command.Env = make([]string, 0, len(os.Environ()))
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "HERDR_SESSION",
			"HERDR_SOCKET_PATH",
			"HERDR_WORKSPACE_ID",
			"HERDR_TAB_ID",
			"HERDR_PANE_ID":
			continue
		}
		command.Env = append(command.Env, variable)
	}
	stdout, err := command.Output()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return stdout, exitError.Stderr, err
	}
	return stdout, nil, err
}

type boundMachineRunner struct {
	machine string
	runner  MachineRunner
}

func (runner boundMachineRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	return runner.runner.RunMachine(ctx, runner.machine, args...)
}

// ForMachine returns an independent client bound to a saved Herdr machine.
func (client Client) ForMachine(machine string) (Client, error) {
	if !healthAtom(machine, false) || strings.HasPrefix(machine, "-") {
		return Client{}, fmt.Errorf("%w: invalid Herdr machine", ErrResponse)
	}
	if client.MachineRunner == nil {
		return Client{}, fmt.Errorf("%w: no machine runner", ErrCommand)
	}
	client.Runner = boundMachineRunner{machine: machine, runner: client.MachineRunner}
	client.SessionRunner = nil
	client.machine, client.session = machine, ""
	return client, nil
}

// Machine identifies the saved machine selected by this client.
func (client Client) Machine() string { return client.machine }

// ForEndpoint selects a local session or a saved machine's recorded session.
func (client Client) ForEndpoint(machine, session string) (Client, error) {
	if machine == "" {
		return client.ForSession(session)
	}
	if !healthAtom(session, false) {
		return Client{}, ErrResponse
	}
	bound, err := client.ForMachine(machine)
	if err != nil {
		return Client{}, err
	}
	bound.session = session
	return bound, nil
}

// ParseEndpoint validates a pane's complete machine and session identity.
func ParseEndpoint(machine, session, paneID string) (Target, error) {
	target, err := ParseTarget(session, paneID)
	if err != nil || machine == "" {
		return target, err
	}
	if _, err := ParseMachineTarget(machine, paneID); err != nil {
		return Target{}, err
	}
	target.machine = machine
	return target, nil
}

// Machine returns the saved machine for a remote health target.
func (target Target) Machine() string { return target.machine }

// RunScoped preserves raw responses for commands whose callers validate absence.
func (client Client) RunScoped(
	ctx context.Context,
	session string,
	args ...string,
) ([]byte, []byte, error) {
	if !healthAtom(session, false) || len(args) == 0 {
		return nil, nil, ErrResponse
	}
	if client.machine != "" {
		if client.session != session || client.MachineRunner == nil {
			return nil, nil, ErrResponse
		}
		return client.MachineRunner.RunMachine(ctx, client.machine, args...)
	}
	if client.SessionRunner == nil {
		return nil, nil, ErrResponse
	}
	return client.SessionRunner.RunSession(ctx, session, append(args, "--session", session)...)
}

// RunPresentationCheck retains local routing and isolates the remote exception.
func (client Client) RunPresentationCheck(
	ctx context.Context,
	session string,
	check RemoteCheck,
) ([]byte, []byte, error) {
	arguments, err := remoteCheckArguments(check)
	if err != nil {
		return nil, nil, err
	}
	if client.machine == "" {
		return client.RunScoped(ctx, session, arguments...)
	}
	if client.RemoteChecks == nil || client.session != session {
		return nil, nil, ErrResponse
	}
	return client.RemoteChecks.RunCheck(ctx, client.machine, session, check)
}

// ParseMachineTarget validates a pane on a saved remote machine.
func ParseMachineTarget(machine, paneID string) (Target, error) {
	if !healthAtom(machine, false) || strings.HasPrefix(machine, "-") || !healthAtom(paneID, true) {
		return Target{}, ErrResponse
	}
	return Target{machine: machine, paneID: paneID}, nil
}
