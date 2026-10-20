package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/roman/concierge/pkg/heartbeat"
)

func TestCodexStartupHookChild(t *testing.T) {
	root := os.Getenv("CONCIERGE_START_TEST_ROOT")
	if root == "" {
		t.Skip("native hook subprocess helper")
	}
	payload, _ := json.Marshal(
		map[string]string{
			"session_id":      "019a0000-0000-7000-8000-000000000001",
			"cwd":             filepath.Dir(root),
			"hook_event_name": "SessionStart",
		},
	)
	for _, args := range [][]string{{"primary", "claim"}, {"heartbeat", "start"}} {
		var output bytes.Buffer
		code := Run(
			context.Background(),
			append([]string{"--root", root}, args...),
			bytes.NewReader(payload),
			&output,
			&output,
		)
		if code != 0 {
			t.Fatalf("%v: code=%d output=%s", args, code, &output)
		}
	}
}

func TestCodexStartupHookRegistersAndRecovers(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	ctx := context.Background()
	root := taskRoot(t, t.TempDir())
	// Keep the Unix path short on platforms with a 104-byte sockaddr limit.
	socketRoot, err := os.MkdirTemp("/tmp", "hb-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketRoot)
	socket := filepath.Join(socketRoot, "native.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if wsjson.Read(r.Context(), connection, &request) != nil {
				return
			}
			if request.ID == 0 {
				continue
			}
			result := map[string]any{}
			if request.Method == "thread/read" {
				result["thread"] = map[string]any{
					"id":     "019a0000-0000-7000-8000-000000000001",
					"cwd":    filepath.Dir(root),
					"status": map[string]string{"type": "idle"},
				}
			}
			if wsjson.Write(
				r.Context(),
				connection,
				map[string]any{"id": request.ID, "result": result},
			) != nil {
				return
			}
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	invokeHook := func() {
		t.Helper()
		child := exec.Command(
			os.Getenv("TINCAN_NATIVE_TEST_EXECUTABLE"),
			"-test.run=^TestCodexStartupHookChild$",
		)
		child.Env = append(
			os.Environ(),
			"CONCIERGE_START_TEST_ROOT="+root,
			"CONCIERGE_CODEX_SOCKET="+socket,
		)
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("startup: %v\n%s", err, output)
		}
	}
	invokeHook()
	store := heartbeat.Store{Root: root}
	first, err := store.Read(ctx)
	if err != nil || !first.Enabled || first.Paused || first.Binding == nil ||
		first.Binding.Destination.Server.PID != os.Getpid() {
		t.Fatalf("startup registration = %+v, %v", first, err)
	}
	if err := store.Pause(ctx, "operator pause"); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Read(ctx)
	invokeHook()
	after, err := store.Read(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf(
			"repeat startup changed the paused scheduler: before=%+v after=%+v error=%v",
			before,
			after,
			err,
		)
	}
}

func TestCodexStartupRejectsOtherHookContext(t *testing.T) {
	for _, input := range []string{`{`, `{"hook_event_name":"Stop"}`, `{"hook_event_name":"SessionStart","cwd":"/other"}`} {
		t.Run(input, func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			var output bytes.Buffer
			if code := Run(context.Background(), []string{"--root", root, "heartbeat", "start"}, strings.NewReader(input), &output, &output); code != 1 {
				t.Fatalf("invalid hook returned %d: %s", code, &output)
			}
		})
	}
}
