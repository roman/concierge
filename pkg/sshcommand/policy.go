package sshcommand

import (
	"context"
	"errors"
	"strings"
	"time"
)

func CheckNativePolicy(ctx context.Context, runner Runner, target string) error {
	if runner == nil {
		return ErrNativePolicy
	}
	arguments, err := NativePolicyArguments(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, _, err := runner.Run(ctx, arguments, nil)
	if err != nil || ctx.Err() != nil {
		return ErrNativePolicy
	}
	return ValidateNativePolicy(output)
}

var ErrNativePolicy = errors.New(
	"select an SSH host entry with ForwardAgent no, ForwardX11 no, and no LocalForward, RemoteForward, DynamicForward, Tunnel or SetEnv settings",
)

// ValidateNativePolicy checks settings that Herdr inherits from OpenSSH. Its
// managed configuration changes multiplexing but does not disable forwarding.
func ValidateNativePolicy(data []byte) error {
	required := map[string]bool{
		"hostname":     false,
		"forwardagent": false,
		"forwardx11":   false,
		"tunnel":       false,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ErrNativePolicy
		}
		name := strings.ToLower(fields[0])
		if present, known := required[name]; known {
			if present || len(fields) != 2 {
				return ErrNativePolicy
			}
			required[name] = true
			if name != "hostname" && fields[1] != "no" && fields[1] != "false" {
				return ErrNativePolicy
			}
		}
		switch name {
		case "localforward", "remoteforward", "dynamicforward", "setenv":
			return ErrNativePolicy
		}
	}
	for _, present := range required {
		if !present {
			return ErrNativePolicy
		}
	}
	return nil
}

func NativePolicyArguments(target string) ([]string, error) {
	if !ValidTarget(target) {
		return nil, ErrTarget
	}
	return []string{"-G", "-T", "-o", "BatchMode=yes", "--", target}, nil
}
