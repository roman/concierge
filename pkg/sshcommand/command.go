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

type SystemRunner struct{ Executable string }

func (runner SystemRunner) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	executable := runner.Executable
	if executable == "" {
		executable = "ssh"
	}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = ConnectionEnvironment(os.Environ())
	command.Stdin = input
	command.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		return nil, nil, ErrOutputLimit
	}
	return stdout.Bytes(), stderr.Bytes(), err
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
	bytes.Buffer
	overflow bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := max(0, (256<<10)-buffer.Len())
	if len(data) > remaining {
		buffer.overflow = true
	}
	_, _ = buffer.Buffer.Write(data[:min(len(data), remaining)])
	return len(data), nil
}
