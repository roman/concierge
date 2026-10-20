package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roman/concierge/internal/lockfile"
	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/primary"
)

func (command *cli) runHeartbeatHook(
	ctx context.Context,
	root, selected string,
	input io.Reader,
	output io.Writer,
	resolve func(primary.Hook) (primary.Identity, error),
) int {
	var hook primary.Hook
	if err := json.NewDecoder(io.LimitReader(input, 65536)).Decode(&hook); err != nil {
		return fail(output, 1, err, "pass native Claude lifecycle hook JSON on stdin")
	}
	if hook.Cwd != filepath.Dir(root) {
		return fail(
			output,
			1,
			errors.New("hook cwd does not match the Concierge root"),
			heartbeatHelp,
		)
	}
	identity, err := resolve(hook)
	if err != nil {
		return fail(output, 1, err, primaryHelp)
	}
	if identity.Kind != "claude" {
		return fail(output, 1, errors.New("Claude lifecycle hook required"), heartbeatHelp)
	}
	status, err := primary.Verify(root, identity)
	if err != nil {
		return fail(output, 1, err, primaryHelp)
	}
	if status == primary.Held {
		return 0
	}
	if status != primary.Owner {
		return fail(output, 1, errors.New("primary owner is unavailable"), primaryHelp)
	}
	if err := heartbeat.ValidateClaudeHooks(filepath.Dir(root)); err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	store := heartbeat.Store{Root: root}
	if selected == "heartbeat activity" {
		gate := heartbeat.Unknown
		switch hook.HookEventName {
		case "UserPromptSubmit":
			gate = heartbeat.Busy
		case "Stop":
			gate = heartbeat.Ready
		case "PermissionRequest":
			gate = heartbeat.Permission
		case "StopFailure":
		default:
			return fail(output, 1, errors.New("unsupported activity hook event"), heartbeatHelp)
		}
		if err := retryHookOwner(ctx, func(attempt context.Context) error { return store.RecordActivity(attempt, identity, gate) }); err != nil {
			return fail(output, 1, err, heartbeatHelp)
		}
		return 0
	}
	if hook.HookEventName != "Stop" {
		return fail(
			output,
			1,
			errors.New("heartbeat wait requires a native Stop hook"),
			heartbeatHelp,
		)
	}
	lifetime := command.Heartbeat.Wait.Timeout
	if lifetime <= 0 || lifetime > 7*time.Hour+59*time.Minute {
		return fail(
			output,
			2,
			errors.New("consumer timeout must be positive and at most 7h59m"),
			heartbeatHelp,
		)
	}
	messageRoot, err := rootWithDefault(command.MessageRoot, ".tincan")
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	defer directory.Close()
	lock, err := lockfile.Open(directory, "claude-stop.lock")
	if err != nil {
		return fail(output, 1, err, "another Stop consumer may still be registered")
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return 0
		}
		return fail(output, 1, err, "another Stop consumer may still be registered")
	}
	ctx, cancel := context.WithTimeout(ctx, lifetime)
	defer cancel()
	var binding heartbeat.Binding
	err = retryHookOwner(ctx, func(attempt context.Context) error {
		var registerErr error
		binding, registerErr = store.Register(
			attempt,
			heartbeat.Destination{
				Mode:    heartbeat.Pull,
				Session: identity.SessionID,
				Expires: time.Now().Add(lifetime),
			},
			func(owner primary.Identity) error {
				if owner != identity {
					return errors.New("Stop hook differs from the owning primary")
				}
				return nil
			},
			time.Now(),
		)
		return registerErr
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return fail(output, 1, err, heartbeatHelp)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		event, err := store.OfferPull(ctx, binding.Generation, time.Now())
		if err != nil && !errors.Is(err, primary.ErrClaimBusy) {
			if ctx.Err() != nil {
				return 0
			}
			return fail(output, 1, err, heartbeatHelp)
		}
		if event != nil {
			if _, err := fmt.Fprintln(output, heartbeat.Notification(executable, root, messageRoot, command.Recipient, *event, binding.Generation)); err != nil {
				return 1
			}
			return 2
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

func retryHookOwner(ctx context.Context, action func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		err := action(ctx)
		if !errors.Is(err, primary.ErrClaimBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
