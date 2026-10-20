package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskmeta"
	"golang.org/x/sys/unix"
)

type hostLifecycleCommand struct {
	Timeout time.Duration `default:"15s" help:"Maximum time for shutdown and replacement readiness."`
}

type hostLifecycleMode uint8

const (
	stopHost hostLifecycleMode = iota
	restartHost
)

func (command *cli) runHostLifecycle(
	ctx context.Context,
	root string,
	mode hostLifecycleMode,
	timeout time.Duration,
	output io.Writer,
) int {
	const help = "inspect concierge host status and .concierge/host.log before retrying"
	if timeout <= 0 {
		return fail(output, 2, errors.New("host timeout must be positive"), "use --timeout 15s")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lease, err := hostactivity.Restart(root)
	if err != nil {
		return fail(output, 1, err, help)
	}
	defer lease.Close()
	messageRoot, err := rootWithDefault(command.MessageRoot, ".tincan")
	if err != nil {
		return fail(output, 2, err, help)
	}
	expected := command.hostExpectations
	expected.MessageRoot, expected.Recipient, expected.Session = messageRoot, command.Recipient, command.Host.Session
	socket := filepath.Join(root, "host.sock")
	request := hostservice.Request{
		Operation:    hostservice.HostStatus,
		Status:       &hostservice.StatusRequest{},
		Expectations: &expected,
	}
	response, err := hostservice.Call(ctx, socket, request)
	if err != nil {
		return fail(output, 1, err, help)
	}
	if response.Outcome != hostservice.Completed {
		return fail(output, 1, errors.New(response.Error), help)
	}
	status := response.Status
	if status.Root != root {
		return fail(output, 1, errors.New("running host uses a different state root"), help)
	}
	var process *exec.Cmd
	var log *os.File
	if mode == restartHost {
		if command.Host.Recovery != nil {
			status.Recovery = *command.Host.Recovery
		}
		process, log, err = replacementHost(status)
		if err != nil {
			return fail(output, 1, err, help)
		}
		defer log.Close()
	}
	request.Operation = hostservice.HostStop
	response, err = hostservice.Call(ctx, socket, request)
	if err != nil {
		return fail(output, 1, err, help)
	}
	if response.Outcome != hostservice.Completed {
		return fail(output, 1, errors.New(response.Error), help)
	}
	if err := awaitHostDeparture(ctx, root); err != nil {
		return fail(output, 1, err, help)
	}
	if mode == stopHost {
		fmt.Fprintln(output, "status: stopped\nworkers: unchanged")
		return 0
	}
	if err := process.Start(); err != nil {
		return fail(output, 1, err, help)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	for {
		response, err := hostservice.Call(
			ctx,
			socket,
			hostservice.Request{
				Operation:    hostservice.HostStatus,
				Status:       &hostservice.StatusRequest{},
				Expectations: &expected,
			},
		)
		if err == nil && response.Outcome == hostservice.Completed {
			if err := checkReplacementStatus(response.Status, status.Recovery); err != nil {
				return fail(
					output,
					1,
					err,
					help,
				)
			}
			fmt.Fprintf(
				output,
				"status: restarted\nhost_revision: %s\nbinary_revision: %s\nworkers: unchanged\n",
				quote(revision),
				quote(revision),
			)
			return 0
		}
		select {
		case err := <-done:
			return fail(
				output,
				1,
				fmt.Errorf("replacement host exited before readiness: %v", err),
				help,
			)
		case <-ctx.Done():
			return fail(output, 1, ctx.Err(), help)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func checkReplacementStatus(
	status *hostservice.StatusResult,
	recovery hostservice.RecoveryPolicy,
) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if status.Executable != executable || status.Revision != revision {
		return errors.New("another host became ready during replacement")
	}
	if status.Recovery.Effective() != recovery.Effective() {
		return errors.New("replacement host uses a different recovery policy")
	}
	return nil
}

func awaitHostDeparture(ctx context.Context, root string) error {
	for {
		lease, err := hostactivity.Departed(root)
		if err == nil {
			return lease.Close()
		}
		if !errors.Is(err, hostactivity.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func replacementHost(status *hostservice.StatusResult) (*exec.Cmd, *os.File, error) {
	if _, err := taskmeta.ParseAbsolutePath(status.HerdrExecutable); err != nil {
		return nil, nil, err
	}
	if _, err := resolveHerdrExecutable(status.HerdrExecutable); err != nil {
		return nil, nil, err
	}
	monitor := status.Supervision
	if monitor.Interval <= 0 || monitor.RestartWindow <= 0 || monitor.RestartTolerance < 0 {
		return nil, nil, errors.New(
			"host does not report restart settings; inspect and stop the older host manually",
		)
	}
	for _, name := range []string{"tincan", "git", "treehouse"} {
		if _, err := exec.LookPath(name); err != nil {
			return nil, nil, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	args := []string{
		"--root",
		status.Root,
		"--message-root",
		status.MessageRoot,
		"--recipient",
		status.Recipient,
		"host",
		"--recovery",
		string(status.Recovery.Effective()),
		"--session",
		status.Session,
		"--herdr",
		status.HerdrExecutable,
		"--interval",
		monitor.Interval.String(),
		"--restart-tolerance",
		strconv.Itoa(monitor.RestartTolerance),
		"--restart-window",
		monitor.RestartWindow.String(),
		"serve",
	}
	process := exec.Command(executable, args...)
	process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	fd, err := unix.Open(
		filepath.Join(status.Root, "host.log"),
		unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		0600,
	)
	if err != nil {
		return nil, nil, err
	}
	log := os.NewFile(uintptr(fd), "host.log")
	info, err := log.Stat()
	if err != nil || !info.Mode().IsRegular() {
		log.Close()
		return nil, nil, errors.New("host.log must be a regular file")
	}
	if err := log.Chmod(0600); err != nil {
		log.Close()
		return nil, nil, err
	}
	process.Stdout, process.Stderr = log, log
	return process, log, nil
}
