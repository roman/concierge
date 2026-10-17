package remoteworker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrWorktreeBusy = errors.New("remote worktree has an active process; stop it before cleanup")

var ErrWorktreeQuietUnconfirmed = errors.New(
	"remote worktree process check is unconfirmed; verify that lsof is available and can inspect the worktree",
)

func checkWorktreeQuiet(ctx context.Context, executable, directory string) error {
	if directory == "/" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory ||
		canonicalRemoteWorktree(directory) != nil {
		return ErrWorktreeOwnership
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return ErrWorktreeOwnership
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-F", "p", "-a", "-d", "cwd", "+D", directory)
	command.Env = sshcommand.ConnectionEnvironment(os.Environ())
	command.WaitDelay = time.Second
	var output, diagnostics outputPresence
	command.Stdout, command.Stderr = &output, &diagnostics
	err = command.Run()
	if ctx.Err() != nil || diagnostics.present {
		return ErrWorktreeQuietUnconfirmed
	}
	if err == nil && output.present {
		return ErrWorktreeBusy
	}
	var exited *exec.ExitError
	// lsof returns one when its selection matches no process. Diagnostics or
	// partial output make that result uncertain, even with the same exit code.
	if errors.As(err, &exited) && exited.ExitCode() == 1 && !output.present {
		return nil
	}
	return ErrWorktreeQuietUnconfirmed
}

type outputPresence struct{ present bool }

func (output *outputPresence) Write(data []byte) (int, error) {
	output.present = output.present || len(data) != 0
	return len(data), nil
}
