package remoteworker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrTunnelNotStarted = errors.New("SSH tunnel process was not started")

type SSHProcesses struct {
	Executable string
	Inspector  sshcommand.Runner
}

func (driver SSHProcesses) Start(
	ctx context.Context,
	spec TunnelSpec,
	localSocket string,
) (primary.ProcessToken, error) {
	arguments, err := TunnelArguments(spec, localSocket)
	if err != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted, err)
	}
	inspection, _ := TunnelConfigArguments(spec, localSocket)
	runner := driver.Inspector
	if runner == nil {
		runner = sshcommand.SystemRunner{Executable: driver.Executable}
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, stderr, err := runner.Run(check, inspection, nil)
	if err != nil || len(stderr) != 0 || check.Err() != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted, ErrTunnelUnreachable)
	}
	if err := ValidateTunnelConfiguration(stdout); err != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted, err)
	}
	if _, err := primary.CaptureProcessToken(os.Getpid()); err != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted, primary.ErrProcessOwnership)
	}
	if err := ctx.Err(); err != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted, err)
	}
	executable := driver.Executable
	if executable == "" {
		executable = "ssh"
	}
	// The host may restart while its worker remains active. A detached SSH child
	// must survive the host context and is recovered through its persisted token.
	command := exec.Command(executable, arguments...)
	command.Env = sshcommand.ConnectionEnvironment(os.Environ())
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return primary.ProcessToken{}, errors.Join(ErrTunnelNotStarted,
			errors.New("cannot start SSH; check the system SSH executable"))
	}
	token, err := primary.CaptureProcessToken(command.Process.Pid)
	if err != nil {
		_ = command.Process.Kill()
	}
	go func() { _ = command.Wait() }()
	if err != nil {
		return primary.ProcessToken{}, ErrTunnelOwnership
	}
	return token, nil
}

func (SSHProcesses) Status(token primary.ProcessToken) (primary.ProcessStatus, error) {
	return primary.ProbeProcessToken(token)
}

func (driver SSHProcesses) Stop(ctx context.Context, token primary.ProcessToken) error {
	if err := token.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Acquire the process handle before checking its token. On Linux the handle
	// pins the process incarnation, so later PID reuse cannot redirect a signal.
	process, handleError := os.FindProcess(token.PID)
	if process != nil {
		defer process.Release()
	}
	status, err := driver.Status(token)
	if err != nil {
		return ErrTunnelOwnership
	}
	if status == primary.ProcessExited {
		return nil
	}
	if handleError != nil || status != primary.ProcessRunning {
		return ErrTunnelOwnership
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return ErrTunnelOwnership
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := driver.Status(token)
		if err != nil {
			return ErrTunnelOwnership
		}
		if status == primary.ProcessExited {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrTunnelOwnership
		case <-ticker.C:
		}
	}
}
