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
	return client, nil
}

// ParseMachineTarget validates a pane on a saved remote machine.
func ParseMachineTarget(machine, paneID string) (Target, error) {
	if !healthAtom(machine, false) || strings.HasPrefix(machine, "-") || !healthAtom(paneID, true) {
		return Target{}, ErrResponse
	}
	return Target{machine: machine, paneID: paneID}, nil
}
