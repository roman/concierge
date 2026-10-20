package codexqueue

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

// Opt-in acceptance uses a new native process and private state. It never
// discovers an existing daemon or inherits its endpoint.
func TestNativeCodexUnixRegistration(t *testing.T) {
	binary := os.Getenv("CONCIERGE_NATIVE_CODEX")
	if binary == "" {
		t.Skip("set CONCIERGE_NATIVE_CODEX to test a disposable installed app-server")
	}
	version, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("native version: %v: %s", err, version)
	}
	t.Logf("native version: %s", version)
	root, err := os.MkdirTemp("", "cq-native-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(root, "server.sock")
	log, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command := exec.CommandContext(ctx, binary, "app-server", "--listen", "unix://"+path)
	command.Env = append(os.Environ(), "CODEX_HOME="+root)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		select {
		case <-ctx.Done():
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("native startup: %v: %s", ctx.Err(), data)
		case <-time.After(10 * time.Millisecond):
		}
	}
	client, err := Open(ctx, path, primary.ProcessToken{})
	if err != nil {
		info, _ := os.Lstat(path)
		target, _ := os.Readlink(path)
		t.Fatalf("native endpoint mode %v, link %q: %v", info.Mode(), target, err)
	}
	defer client.Close()
	if client.Server.PID != command.Process.Pid {
		t.Fatalf(
			"native endpoint belongs to PID %d, launched PID %d",
			client.Server.PID,
			command.Process.Pid,
		)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := client.call(ctx, "thread/start", map[string]any{"cwd": root, "ephemeral": true}, &started); err != nil {
		t.Fatal(err)
	}
	if started.Thread.ID == "" {
		t.Fatal("native thread UUID missing")
	}
	if err := client.Verify(ctx, started.Thread.ID, root); err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"verified isolated server PID %d and thread %s; no model turn or visible output claimed",
		client.Server.PID,
		started.Thread.ID,
	)
}
