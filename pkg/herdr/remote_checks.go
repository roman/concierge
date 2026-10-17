package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
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
	var arguments []string
	switch check {
	case SessionInventory:
		arguments = []string{"session", "list", "--json"}
	case ForegroundViewer:
		arguments = []string{"terminal", "title", "clear"}
	default:
		return nil, nil, fmt.Errorf("%w: unsupported remote check", ErrResponse)
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
	for index := range command {
		command[index] = shellQuote(command[index])
	}
	// Disable configured forwarding as well as agent forwarding for these checks.
	// The worker tunnel has its own separately managed SSH process.
	sshArguments := []string{
		"-S", "none", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes",
		"-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes",
		"--", profile.Target, strings.Join(command, " "),
	}
	stdout, stderr, err := checks.Runner.Run(ctx, sshArguments...)
	if err != nil || len(stderr) != 0 || ctx.Err() != nil {
		return nil, nil, fmt.Errorf("%w: remote presentation check failed", ErrUnreachable)
	}
	return stdout, nil, nil
}

func validSSHDestination(target string) bool {
	return target != "" && !strings.HasPrefix(target, "-") &&
		strings.IndexFunc(
			target,
			func(character rune) bool { return unicode.IsControl(character) || unicode.IsSpace(character) },
		) < 0
}
