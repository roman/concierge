package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/internal/nativetest"
	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/tincan/pkg/inbox"
)

func TestClaudeLifecycleDeliversOnlyFromReadyStop(t *testing.T) {
	if nativetest.RunUnderKind(t, "claude") {
		return
	}
	ctx := context.Background()
	root := taskRoot(t, t.TempDir())
	messageRoot := t.TempDir()
	t.Setenv("CLAUDE_CODE_SESSION_ID", fmt.Sprintf("fixture-%d", os.Getpid()))
	t.Setenv("CLAUDE_PID", fmt.Sprint(os.Getpid()))
	claimNativeParent(t, root)
	installClaudeHookFixture(t, filepath.Dir(root))
	store := heartbeat.Store{Root: root}
	if err := store.Enable(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err := store.Schedule(
		ctx,
		time.Now(),
		heartbeat.Observation{Active: true, Text: "snapshot"},
		heartbeat.Settings{Interval: "1m"},
	)
	if err != nil {
		t.Fatal(err)
	}
	id := state.Pending.ID
	recipient, _ := inbox.ParseRecipient("concierge")
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	hook := func(event string, arguments ...string) (string, int) {
		t.Helper()
		payload, _ := json.Marshal(
			primary.Hook{
				SessionID:     os.Getenv("CLAUDE_CODE_SESSION_ID"),
				Cwd:           filepath.Dir(root),
				HookEventName: event,
			},
		)
		args, _ := json.Marshal(
			append(
				[]string{"--root", root, "--message-root", messageRoot, "heartbeat"},
				arguments...),
		)
		executable := os.Getenv("TINCAN_NATIVE_TEST_EXECUTABLE")
		command := exec.Command(executable, "-test.run=^TestHeartbeatClaudeHookCommand$")
		command.Env = append(
			os.Environ(),
			"CONCIERGE_TEST_CLAUDE_ARGS="+string(args),
			"CONCIERGE_TEST_CLAUDE_HOOK="+string(payload),
		)
		output, err := command.CombinedOutput()
		code := 0
		if err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				code = failure.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		return string(output), code
	}
	ownerSession := os.Getenv("CLAUDE_CODE_SESSION_ID")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "secondary")
	if output, code := hook("UserPromptSubmit", "activity"); code != 0 || output != "" {
		t.Fatalf("secondary prompt blocked: %d %s", code, output)
	}
	state, _ = store.Read(ctx)
	if state.Activity != nil {
		t.Fatal("secondary session changed primary activity")
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", ownerSession)
	for _, event := range []string{"UserPromptSubmit", "PermissionRequest", "StopFailure"} {
		if output, code := hook(event, "activity"); code != 0 {
			t.Fatalf("%s: %d %s", event, code, output)
		}
		if output, code := hook("Stop", "wait", "--timeout", "50ms"); code != 0 || output != "" {
			t.Fatalf("unsafe offer after %s: %d %s", event, code, output)
		}
	}
	if output, code := hook("Stop", "activity"); code != 0 {
		t.Fatalf("ready: %d %s", code, output)
	}
	output, code := hook("Stop", "wait", "--timeout", "1m")
	if code != 2 || !strings.Contains(output, "heartbeat claim '"+id+"'") {
		t.Fatalf("ready offer: %d %s", code, output)
	}
	state, _ = store.Read(ctx)
	generation := state.Binding.Generation
	if state.Pending == nil || state.Receipt != nil || state.Pending.Attempt.Count != 1 {
		t.Fatalf("offer consumed event: %+v", state)
	}
	if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err != nil {
		t.Fatal(err)
	}
	afterPushTick, _ := store.Read(ctx)
	if afterPushTick.Delivery != "offered" {
		t.Fatalf("push tick overwrote pull phase: %s", afterPushTick.Delivery)
	}
	attempt := state.Pending.Attempt
	mutateHeartbeatRecord(t, root, func(current *heartbeat.State) { current.Pending.Attempt = nil })
	before, _ := os.ReadFile(filepath.Join(root, "supervisor.json"))
	if event, err := store.OfferPull(ctx, generation, time.Now()); err == nil || event != nil ||
		!strings.Contains(err.Error(), "consumer was replaced") {
		t.Fatalf("foreign consumer offered an event: %+v, %v", event, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "supervisor.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("rejected consumer changed the pending event")
	}
	mutateHeartbeatRecord(
		t,
		root,
		func(current *heartbeat.State) { current.Pending.Attempt = attempt },
	)
	if output, code := hook("Stop", "wait", "--timeout", "50ms"); code != 0 || output != "" {
		t.Fatalf("duplicate offer: %d %s", code, output)
	}
	if output, code := hook("UserPromptSubmit", "activity"); code != 0 {
		t.Fatalf("busy: %d %s", code, output)
	}
	var claimed bytes.Buffer
	args := []string{
		"--root",
		root,
		"--message-root",
		messageRoot,
		"heartbeat",
		"claim",
		id,
		"--generation",
		generation,
	}
	if code := Run(ctx, args, strings.NewReader(""), &claimed, &claimed); code != 0 {
		t.Fatalf("claim: %d %s", code, claimed.String())
	}
	token := heartbeatField(t, claimed.String(), "claim_token")
	if err := store.AcknowledgeCurrent(ctx, id, generation, token, "operator-output:fixture", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordActivity(ctx, primary.Identity{Kind: "claude", PID: 1}, heartbeat.Ready); err == nil {
		t.Fatal("foreign activity accepted")
	}
	if err := store.RecordActivity(ctx, state.Binding.Owner, heartbeat.ActivityGate("invalid")); err == nil {
		t.Fatal("invalid activity accepted")
	}
	current, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previousConsumer := *current.Binding.Consumer
	type consumerResult struct {
		output string
		code   int
	}
	finished := make(chan consumerResult, 1)
	go func() {
		output, code := hook("Stop", "wait", "--timeout", "1s")
		finished <- consumerResult{output, code}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := store.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if current.Binding.Consumer != nil && *current.Binding.Consumer != previousConsumer {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first Stop consumer did not register")
		}
		time.Sleep(time.Millisecond)
	}
	if output, code := hook("Stop", "wait", "--timeout", "50ms"); code != 0 || output != "" {
		t.Fatalf("overlapping consumer: %d %s", code, output)
	}
	if result := <-finished; result.code != 0 || result.output != "" {
		t.Fatalf("first consumer: %+v", result)
	}
}

func TestHeartbeatClaudeHookCommand(t *testing.T) {
	data := os.Getenv("CONCIERGE_TEST_CLAUDE_ARGS")
	if data == "" {
		t.Skip("subprocess helper")
	}
	var args []string
	if err := json.Unmarshal([]byte(data), &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(
		Run(
			context.Background(),
			args,
			strings.NewReader(os.Getenv("CONCIERGE_TEST_CLAUDE_HOOK")),
			os.Stdout,
			os.Stderr,
		),
	)
}

func installClaudeHookFixture(t *testing.T, project string) {
	t.Helper()
	for _, directory := range []string{".agents/hooks", ".claude"} {
		if err := os.MkdirAll(filepath.Join(project, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"concierge-claude-activity", "concierge-claude-stop"} {
		if err := os.WriteFile(filepath.Join(project, ".agents/hooks", name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	activity := map[string]any{
		"type":    "command",
		"command": heartbeat.ClaudeHookCommand("concierge-claude-activity"),
	}
	busy := map[string]any{
		"type":      "command",
		"command":   heartbeat.ClaudeHookCommand("concierge-claude-activity"),
		"onFailure": "block",
	}
	stop := map[string]any{
		"type":        "command",
		"command":     heartbeat.ClaudeHookCommand("concierge-claude-stop"),
		"asyncRewake": true,
		"timeout":     28800,
	}
	settings := map[string]any{"hooks": map[string]any{
		"UserPromptSubmit": []any{map[string]any{"hooks": []any{busy}}},
		"Stop":             []any{map[string]any{"hooks": []any{activity, stop}}},
	}}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(project, ".claude", "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
