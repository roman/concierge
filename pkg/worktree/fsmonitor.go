package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/gitcommand"
	"github.com/roman/concierge/pkg/sshcommand"
)

// StopFsmonitor requires the caller to confirm checkout ownership and HEAD first.
// The process check must follow this call so Git's own daemon cannot block return.
func (client Client) StopFsmonitor(ctx context.Context, directory string) error {
	if !filepath.IsAbs(client.Executable) || !filepath.IsAbs(directory) || directory == "/" {
		return ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Only boolean true selects Git's built-in daemon. Other values select a
	// hook or disable monitoring, including on platforms without daemon support.
	configuration := exec.CommandContext(ctx, client.Executable,
		"-C", directory, "config", "--get", "core.fsmonitor")
	configuration.Env = sshcommand.ConnectionEnvironment(os.Environ())
	configuration.WaitDelay = time.Second
	configured, err := configuration.Output()
	var missing *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &missing) && missing.ExitCode() == 1 &&
		len(configured) == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect checkout fsmonitor: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(string(configured))) {
	case "true", "yes", "on", "1":
	default:
		return nil
	}
	command := exec.CommandContext(ctx, client.Executable,
		gitcommand.Arguments(directory, "fsmonitor--daemon", "stop")...)
	command.Env = append(sshcommand.ConnectionEnvironment(os.Environ()), "LC_ALL=C")
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 128 {
		switch strings.TrimSpace(string(output)) {
		case "fatal: fsmonitor--daemon is not running",
			"fatal: fsmonitor--daemon not supported on this platform":
			return nil
		}
	}
	return fmt.Errorf("stop checkout fsmonitor: %w: %s", err, strings.TrimSpace(string(output)))
}
