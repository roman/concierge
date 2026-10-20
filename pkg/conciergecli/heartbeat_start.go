package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/codexqueue"
	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/primary"
)

func runHeartbeatStart(ctx context.Context, root string, input io.Reader, output io.Writer) int {
	var hook primary.Hook
	if err := json.NewDecoder(io.LimitReader(input, 65536)).Decode(&hook); err != nil {
		return fail(output, 1, err, "pass the native SessionStart payload on stdin")
	}
	if hook.HookEventName != "SessionStart" || hook.Cwd != filepath.Dir(root) {
		return fail(
			output,
			1,
			errors.New("Codex startup requires the root SessionStart hook"),
			primaryHelp,
		)
	}
	identity, err := primary.CurrentHookIdentity(hook)
	if err != nil {
		return fail(output, 1, err, primaryHelp)
	}
	if identity.Kind != "codex" {
		return fail(
			output,
			1,
			errors.New("Codex startup requires a native Codex hook"),
			primaryHelp,
		)
	}
	socket := os.Getenv("CONCIERGE_CODEX_SOCKET")
	if socket == "" {
		return fail(
			output,
			1,
			errors.New("owning Codex endpoint is unavailable"),
			"enter the adopted Concierge shell and launch codex; inspect concierge.codex.enable",
		)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	server := primary.ProcessToken{
		PID:          identity.PID,
		StartTicks:   identity.StartTicks,
		BootID:       identity.BootID,
		PIDNamespace: identity.PIDNamespace,
	}
	client, err := codexqueue.Open(ctx, socket, server)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"restart codex through the adopted Concierge shell; preserve pending events",
		)
	}
	defer client.Close()
	store := heartbeat.Store{Root: root}
	_, err = store.RegisterCodexHook(
		ctx,
		identity,
		heartbeat.Destination{
			Mode:   heartbeat.Push,
			Socket: socket,
			Thread: hook.SessionID,
			Server: client.Server,
		},
		func(primary.Identity) error {
			return client.Verify(ctx, hook.SessionID, hook.Cwd)
		},
		time.Now(),
	)
	if err != nil {
		return fail(output, 1, err, "inspect concierge heartbeat show; preserve pending events")
	}
	if err := store.EnableForStartup(ctx, time.Now()); err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	fmt.Fprintln(output, "status: registered\nscheduler: host_owned")
	return 0
}
