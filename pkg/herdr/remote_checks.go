package herdr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrUnreachable = errors.New("remote machine is unreachable")

// SavedMachine preserves the SSH destination and session from Herdr's catalog.
type SavedMachine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session"`
	Enabled bool   `json:"enabled"`
}

type MachineResolver interface {
	Resolve(context.Context, string) (SavedMachine, error)
}

type RemoteCheck string

const (
	SessionInventory RemoteCheck = "session_inventory"
	ForegroundViewer RemoteCheck = "foreground_viewer"
)

// RemoteChecks isolates the two checks absent from Herdr's machine forwarding.
type RemoteChecks interface {
	RunCheck(context.Context, string, string, RemoteCheck) ([]byte, []byte, error)
}

// SSHRemoteChecks runs fixed local Herdr commands on the saved SSH destination.
type SSHRemoteChecks struct {
	Machines MachineResolver
	Runner   Runner
}

func (checks SSHRemoteChecks) RunCheck(
	ctx context.Context,
	machine, session string,
	check RemoteCheck,
) ([]byte, []byte, error) {
	if checks.Machines == nil || checks.Runner == nil || !healthAtom(session, false) {
		return nil, nil, ErrResponse
	}
	arguments, err := remoteCheckArguments(check)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	profile, err := checks.Machines.Resolve(ctx, machine)
	if err != nil {
		return nil, nil, err
	}
	if !profile.Enabled || profile.Session != session || !validSSHDestination(profile.Target) {
		return nil, nil, fmt.Errorf("%w: saved machine destination or session changed", ErrResponse)
	}
	command := append([]string{"herdr", "--session", session}, arguments...)
	sshArguments, err := sshcommand.TrustedBatchArguments(profile.Target, command...)
	if err != nil {
		return nil, nil, err
	}
	stdout, stderr, err := checks.Runner.Run(ctx, sshArguments...)
	if err != nil || len(stderr) != 0 || ctx.Err() != nil {
		return nil, nil, fmt.Errorf("%w: remote presentation check failed", ErrUnreachable)
	}
	return stdout, nil, nil
}

func validSSHDestination(target string) bool {
	return sshcommand.ValidTarget(target)
}

type systemSSHCheckRunner struct{}

func (systemSSHCheckRunner) Run(ctx context.Context, arguments ...string) ([]byte, []byte, error) {
	return (sshcommand.SystemRunner{}).Run(ctx, arguments, nil)
}

func remoteCheckArguments(check RemoteCheck) ([]string, error) {
	switch check {
	case SessionInventory:
		return []string{"session", "list", "--json"}, nil
	case ForegroundViewer:
		return []string{"terminal", "title", "clear"}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported remote check", ErrResponse)
	}
}
