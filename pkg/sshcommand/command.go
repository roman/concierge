// Package sshcommand builds bounded system SSH invocations without forwarding credentials.
package sshcommand

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"
)

var ErrTarget = errors.New("invalid SSH destination")
var ErrOutputLimit = errors.New("SSH response exceeds the size limit")

func ValidTarget(target string) bool {
	return target != "" && !strings.HasPrefix(target, "-") &&
		strings.IndexFunc(target, func(character rune) bool {
			return unicode.IsControl(character) || unicode.IsSpace(character)
		}) < 0
}

func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func BatchArguments(target string, command ...string) ([]string, error) {
	if !ValidTarget(target) || len(command) == 0 {
		return nil, ErrTarget
	}
	quoted := make([]string, len(command))
	for index, argument := range command {
		if strings.ContainsRune(argument, 0) {
			return nil, ErrTarget
		}
		quoted[index] = Quote(argument)
	}
	return []string{
		"-S", "none", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes",
		"-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes",
		"-o", "ForwardX11=no", "-o", "ForwardX11Trusted=no",
		"-o", "SetEnv=CONCIERGE_SSH_TRANSPORT=1",
		"--", target, strings.Join(quoted, " "),
	}, nil
}

// TrustedBatchArguments requires an existing host-key trust decision and leaves
// known_hosts unchanged during remote setup and acceptance.
func TrustedBatchArguments(target string, command ...string) ([]string, error) {
	arguments, err := BatchArguments(target, command...)
	if err != nil {
		return nil, err
	}
	return append(
		[]string{"-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no"},
		arguments...), nil
}

type Runner interface {
	Run(context.Context, []string, io.Reader) ([]byte, []byte, error)
}

// StreamingRunner transfers binary responses to a caller-owned file without
// applying the small control-response limit to repository bundles.
type StreamingRunner interface {
	RunTo(context.Context, []string, io.Reader, io.Writer) ([]byte, error)
}

type SystemRunner struct{ Executable string }

func (runner SystemRunner) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	var stdout limitedBuffer
	diagnostics, err := runner.RunTo(ctx, arguments, input, &stdout)
	if stdout.overflow || errors.Is(err, ErrOutputLimit) {
		return nil, nil, ErrOutputLimit
	}
	return stdout.Bytes(), diagnostics, err
}

func (runner SystemRunner) RunTo(
	ctx context.Context,
	arguments []string,
	input io.Reader,
	output io.Writer,
) ([]byte, error) {
	if output == nil {
		return nil, errors.New("SSH transfer requires an output destination")
	}
	executable := runner.Executable
	if executable == "" {
		executable = "ssh"
	}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = ConnectionEnvironment(os.Environ())
	command.Stdin = input
	command.WaitDelay = time.Second
	var stderr limitedBuffer
	command.Stdout, command.Stderr = output, &stderr
	err := command.Run()
	if stderr.overflow {
		return nil, ErrOutputLimit
	}
	return stderr.Bytes(), err
}

func ConnectionEnvironment(environment []string) []string {
	allowed := []string{
		"PATH",
		"HOME",
		"USER",
		"LOGNAME",
		"SHELL",
		"SSH_AUTH_SOCK",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
	}
	var selected []string
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if slices.Contains(allowed, key) {
			selected = append(selected, entry)
		}
	}
	return selected
}

type limitedBuffer struct {
	data     bytes.Buffer
	overflow bool
}

func (buffer *limitedBuffer) Bytes() []byte { return buffer.data.Bytes() }

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := max(0, (256<<10)-buffer.data.Len())
	if len(data) > remaining {
		buffer.overflow = true
	}
	_, _ = buffer.data.Write(data[:min(len(data), remaining)])
	return len(data), nil
}
