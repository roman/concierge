package sshcommand

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

type Authentication string

const (
	RemoteCredentials Authentication = "remote"
	ForwardedAgent    Authentication = "agent-forwarding"
)

func (authentication Authentication) Validate() error {
	switch authentication {
	case "", RemoteCredentials, ForwardedAgent:
		return nil
	default:
		return errors.New("authentication must be remote or agent-forwarding")
	}
}

// TrustedAuthenticationArguments applies only the operator-selected machine mode.
// The ordinary argument builder continues to disable forwarding by default.
func TrustedAuthenticationArguments(
	target string,
	authentication Authentication,
	command ...string,
) ([]string, error) {
	if err := authentication.Validate(); err != nil {
		return nil, err
	}
	arguments, err := TrustedBatchArguments(target, command...)
	if err != nil {
		return nil, err
	}
	if authentication == ForwardedAgent {
		for index, argument := range arguments {
			if argument == "ForwardAgent=no" {
				arguments[index] = "ForwardAgent=yes"
				break
			}
		}
	}
	return arguments, nil
}

var ErrPrimaryAgent = errors.New("primary SSH agent is unavailable or has no usable keys")

func CheckPrimaryAgent(ctx context.Context) error {
	return CheckAgentSocket(ctx, os.Getenv("SSH_AUTH_SOCK"))
}

func CheckAgentSocket(ctx context.Context, socket string) error {
	if socket == "" {
		return ErrPrimaryAgent
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ssh-add", "-l")
	command.Env = append(ConnectionEnvironment(os.Environ()), "SSH_AUTH_SOCK="+socket)
	command.WaitDelay = time.Second
	// Fingerprints and comments are unnecessary for the readiness decision.
	if err := command.Run(); err != nil {
		return ErrPrimaryAgent
	}
	return nil
}
