package codexqueue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

// This opt-in trial uses a deterministic local provider. It preserves native
// approval and sandbox controls and never attaches to an existing daemon.
func TestNativeCodexQueueDispatch(t *testing.T) {
	runNativeCodexQueueDispatch(t, "")
}

func TestNativeCodexLegacyQueueDispatch(t *testing.T) {
	runNativeCodexQueueDispatch(t, "legacy")
}

func runNativeCodexQueueDispatch(t *testing.T, historyMode string) {
	t.Helper()
	binary := os.Getenv("CONCIERGE_NATIVE_CODEX")
	if binary == "" {
		t.Skip("set CONCIERGE_NATIVE_CODEX for disposable native acceptance")
	}
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		count := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(
			w,
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"fixture-%d\"}}\n\n",
			count,
		)
		fmt.Fprintf(
			w,
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"fixture-message-%d\",\"content\":[{\"type\":\"output_text\",\"text\":\"NATIVE_FIXTURE_TURN_%d\"}]}}\n\n",
			count,
			count,
		)
		fmt.Fprintf(
			w,
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture-%d\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n",
			count,
		)
	}))
	defer provider.Close()
	root, err := os.MkdirTemp("", "cq-dispatch-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	defer func() {
		if t.Failed() {
			for _, name := range []string{"server.log", "tui.stderr"} {
				if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
					t.Logf("%s:\n%s", name, data)
				}
			}
		}
	}()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	for _, path := range []string{home, work} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf(
		"model = \"fixture\"\nmodel_provider = \"fixture\"\napproval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n[features]\nplugins = false\n[model_providers.fixture]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\nsupports_websockets = false\n",
		provider.URL,
	)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path := filepath.Join(root, "server.sock")
	log, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server := exec.CommandContext(ctx, binary, "app-server", "--listen", "unix://"+path)
	server.Env = append(os.Environ(), "CODEX_HOME="+home)
	server.Dir = work
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	client, err := Open(ctx, path, primary.ProcessToken{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Server.PID != server.Process.Pid {
		t.Fatal("server peer differs from launched process")
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	params := map[string]any{"cwd": work}
	if historyMode != "" {
		params["historyMode"] = historyMode
	}
	if err := client.call(ctx, "thread/start", params, &started); err != nil {
		t.Fatal(err)
	}
	if started.ApprovalPolicy != "on-request" || started.Sandbox.Type != "workspaceWrite" {
		t.Fatalf("native controls differ from trial configuration: %+v", started)
	}
	thread := started.Thread.ID
	if err := client.Verify(ctx, thread, work); err != nil {
		t.Fatal(err)
	}
	if err := client.call(ctx, "turn/start", map[string]any{"threadId": thread, "input": []any{map[string]string{"type": "text", "text": "Initial local fixture turn"}}}, nil); err != nil {
		t.Fatal(err)
	}
	await := func(count int32) {
		t.Helper()
		for {
			gate, err := client.Readiness(ctx, thread, work)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() >= count && gate == Ready {
				return
			}
			select {
			case <-ctx.Done():
				data, _ := os.ReadFile(log.Name())
				t.Fatalf("native dispatch: %v: %s", ctx.Err(), data)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	await(1)
	ui := os.Getenv("CONCIERGE_NATIVE_UI_LOOP")
	var session string
	if ui != "" {
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
		tuiLog := filepath.Join(root, "tui.stderr")
		command := "CODEX_HOME=" + quote(
			home,
		) + " " + quote(
			binary,
		) + " --remote " + quote(
			"unix://"+path,
		) + " --no-alt-screen resume " + quote(
			thread,
		) + " 2>" + quote(
			tuiLog,
		)
		start := exec.CommandContext(
			ctx,
			ui,
			"start",
			"--tui",
			command,
			"--size",
			"120x40",
			"--label",
			"concierge-native",
		)
		start.Dir = work
		output, err := start.CombinedOutput()
		if err != nil {
			diagnostics, _ := os.ReadFile(tuiLog)
			t.Fatalf("native TUI startup: %v: %s\n%s", err, output, diagnostics)
		}
		session = strings.TrimSpace(string(output))
		defer exec.Command(ui, "stop", session).Run()
		if output, err := exec.CommandContext(ctx, ui, "wait", session, "--text", "NATIVE_FIXTURE_TURN_1", "--timeout", "10").CombinedOutput(); err != nil {
			capture, _ := exec.Command(ui, "text", session).CombinedOutput()
			t.Fatalf(
				"native TUI did not attach without extra approval: %v: %s\n%s",
				err,
				output,
				capture,
			)
		}
	}
	if err := client.Submit(ctx, thread, "native-fixture-queue-1", "QUEUED_IDLE_NATIVE_SENTINEL"); err != nil {
		t.Fatal(err)
	}
	await(2)
	if found, err := client.Contains(ctx, thread, "native-fixture-queue-1"); err != nil || !found {
		t.Fatalf("native submission history: %t, %v", found, err)
	}
	if session != "" {
		if output, err := exec.CommandContext(ctx, ui, "wait", session, "--text", "NATIVE_FIXTURE_TURN_2", "--timeout", "10").CombinedOutput(); err != nil {
			capture, _ := exec.Command(ui, "text", session).CombinedOutput()
			t.Fatalf("queued turn absent from TUI: %v: %s\n%s", err, output, capture)
		}
		capture, err := exec.Command(ui, "text", session).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("native TUI after queued idle dispatch:\n%s", capture)
	}
	t.Logf(
		"server PID %d, exact thread %s: idle queue insertion started the second native model turn",
		server.Process.Pid,
		thread,
	)
}
