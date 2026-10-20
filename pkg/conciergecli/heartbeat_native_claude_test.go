package conciergecli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/tincan/pkg/inbox"
)

func TestNativeClaudeStopConsumer(t *testing.T) {
	binary, concierge := os.Getenv(
		"CONCIERGE_NATIVE_CLAUDE",
	), os.Getenv(
		"CONCIERGE_NATIVE_CONCIERGE",
	)
	if binary == "" || concierge == "" {
		t.Skip(
			"set CONCIERGE_NATIVE_CLAUDE and CONCIERGE_NATIVE_CONCIERGE for isolated native hook acceptance",
		)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	project := t.TempDir()
	root := taskRoot(t, filepath.Join(project, ".concierge"))
	messageRoot := filepath.Join(project, ".tincan")
	store := heartbeat.Store{Root: root}
	if err := store.Enable(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	installClaudeHookFixture(t, project)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	header := "#!/usr/bin/env bash\nset -euo pipefail\nrepository_root=" + quote(
		project,
	) + "\nconcierge_command=" + quote(
		concierge,
	) + "\n"
	for _, name := range []string{"concierge-claude-activity", "concierge-claude-stop"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "nix", "hooks", name+".sh"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, ".agents", "hooks", name), []byte(header+string(body)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	startPath := filepath.Join(project, ".agents", "hooks", "native-session-start")
	if err := os.WriteFile(startPath, []byte(header+`exec "$concierge_command" --root "$repository_root/.concierge" primary claim`+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	data, _ := os.ReadFile(settingsPath)
	var settings map[string]any
	json.Unmarshal(data, &settings)
	settings["hooks"].(map[string]any)["SessionStart"] = []any{
		map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": quote(startPath)}},
		},
	}
	data, _ = json.Marshal(settings)
	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		count := requests.Add(1)
		if count == 2 {
			body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
			state, stateErr := store.Read(context.Background())
			if err != nil || stateErr != nil || state.Pending == nil ||
				!strings.Contains(
					string(body),
					"heartbeat claim '"+state.Pending.ID+"' --generation '"+state.Binding.Generation+"'",
				) {
				t.Errorf(
					"second native request lacks the published event claim: read=%v state=%v",
					err,
					stateErr,
				)
			}
		}
		if count == 1 {
			state, err := store.Read(context.Background())
			if err != nil || state.Activity == nil || state.Activity.Gate != heartbeat.Busy {
				t.Errorf(
					"first native request lacks synchronous busy gate: %+v, %v",
					state.Activity,
					err,
				)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(
			w,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture-%d\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n",
			count,
		)
		fmt.Fprint(
			w,
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		)
		fmt.Fprintf(
			w,
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"CLAUDE_NATIVE_FIXTURE_%d\"}}\n\n",
			count,
		)
		fmt.Fprint(
			w,
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		)
	}))
	defer provider.Close()
	stdout, err := os.Create(filepath.Join(project, "native.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(project, "native.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	defer func() {
		if t.Failed() {
			for _, file := range []*os.File{stdout, stderr} {
				data, _ := os.ReadFile(file.Name())
				t.Logf("%s:\n%s", filepath.Base(file.Name()), data)
			}
		}
	}()
	command := exec.CommandContext(
		ctx,
		binary,
		"--print",
		"--input-format",
		"stream-json",
		"--output-format",
		"stream-json",
		"--verbose",
		"--include-hook-events",
		"--settings",
		settingsPath,
		"--model",
		"fixture",
		"--tools",
		"",
		"--session-id",
		"019a0000-0000-7000-8000-000000000003",
	)
	command.Dir = project
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "CONCIERGE_WORKER",
			"ANTHROPIC_API_KEY",
			"ANTHROPIC_AUTH_TOKEN",
			"CLAUDE_CODE_OAUTH_TOKEN",
			"CLAUDE_CODE_SESSION_ID",
			"CLAUDE_PID",
			"ANTHROPIC_BASE_URL",
			"CLAUDE_CONFIG_DIR":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(
		command.Env,
		"CLAUDE_CONFIG_DIR="+filepath.Join(project, "config"),
		"ANTHROPIC_API_KEY=local-fixture",
		"ANTHROPIC_BASE_URL="+provider.URL,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	if err := json.NewEncoder(input).Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Initial isolated native fixture turn"}}); err != nil {
		t.Fatal(err)
	}
	for {
		state, err := store.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if requests.Load() >= 1 && state.Binding != nil && state.Activity != nil &&
			state.Activity.Gate == heartbeat.Ready {
			if state.Binding.Consumer == nil {
				t.Fatal("native CLI lacks a consumer process token; rebuild the acceptance binary")
			}
			if err := store.CheckBinding(ctx, state.Binding.Generation, time.Now()); err != nil {
				t.Fatalf("live native consumer unavailable: %v", err)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native Claude did not register a ready Stop consumer")
		case <-time.After(20 * time.Millisecond):
		}
	}
	state, err := store.Schedule(
		ctx,
		time.Now(),
		heartbeat.Observation{Active: true, Text: "Native isolated supervisor event"},
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
	for {
		data, _ := os.ReadFile(stdout.Name())
		if requests.Load() >= 2 && strings.Contains(string(data), "CLAUDE_NATIVE_FIXTURE_2") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native Stop event did not produce a second visible model response")
		case <-time.After(20 * time.Millisecond):
		}
	}
	state, err = store.Read(ctx)
	if err != nil || state.Pending == nil || state.Pending.ID != id || state.Receipt != nil {
		t.Fatalf("native offer consumed unreported output: %+v, %v", state, err)
	}
	t.Logf(
		"native Claude PID %d, exact session %s: asyncRewake Stop produced a second visible fixture response; event %s retained without output ACK",
		command.Process.Pid,
		state.Binding.Destination.Session,
		id,
	)
}
