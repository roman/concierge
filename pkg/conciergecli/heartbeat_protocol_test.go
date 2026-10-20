package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/tincan/pkg/inbox"
)

func TestHeartbeatPublicationProcessingAndOutputProtocol(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	ctx := context.Background()
	root := taskRoot(t, t.TempDir())
	messageRoot := t.TempDir()
	claimNativeParent(t, root)
	thread := "019a0000-0000-7000-8000-000000000001"
	t.Setenv("CODEX_THREAD_ID", thread)
	socket, submissions := heartbeatQueueFixture(t, root, messageRoot, thread)
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		code := Run(
			ctx,
			append([]string{"--root", root, "--message-root", messageRoot}, args...),
			strings.NewReader(""),
			&output,
			&output,
		)
		if code != 0 {
			t.Fatalf("%v: code %d: %s", args, code, output.String())
		}
		return output.String()
	}
	run("heartbeat", "register", "codex", "--socket", socket, "--thread", thread)
	run("heartbeat", "enable")
	run("operator", "ask", "approval", "Approve the current result")
	store := heartbeat.Store{Root: root}
	state, err := store.Schedule(
		ctx,
		time.Now(),
		heartbeat.Observation{Active: true, Text: "stale snapshot"},
		heartbeat.Settings{Interval: "1m", QuietLimit: 5},
	)
	if err != nil {
		t.Fatal(err)
	}
	eventID := state.Pending.ID
	generation := state.Binding.Generation
	recipient, _ := inbox.ParseRecipient("concierge")
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err = store.Read(ctx)
	if err != nil || state.Pending == nil || state.Receipt != nil ||
		state.Pending.Attempt.Phase != "accepted" ||
		submissions.Load() != 1 {
		t.Fatalf("acceptance consumed output: %+v, %v", state, err)
	}
	claimed := run("heartbeat", "claim", eventID, "--generation", generation)
	if !strings.Contains(claimed, "status: current") ||
		!strings.Contains(claimed, "notification:") ||
		!strings.Contains(claimed, "Approve the current result") ||
		strings.Contains(claimed, "stale snapshot") {
		t.Fatal(claimed)
	}
	token := heartbeatField(t, claimed, "claim_token")
	run("heartbeat", "pause", "--reason", "maintenance")
	var failed bytes.Buffer
	args := []string{
		"--root",
		root,
		"--message-root",
		messageRoot,
		"heartbeat",
		"ack",
		eventID,
		"--generation",
		generation,
		"--token",
		token,
		"--output",
		"commentary: posted",
	}
	if code := Run(ctx, args, strings.NewReader(""), &failed, &failed); code != 1 {
		t.Fatalf("paused periodic ack: code %d: %s", code, failed.String())
	}
	run("heartbeat", "resume")
	claimed = run("heartbeat", "claim", eventID, "--generation", generation)
	token = heartbeatField(t, claimed, "claim_token")
	run(
		"heartbeat",
		"ack",
		eventID,
		"--generation",
		generation,
		"--token",
		token,
		"--output",
		"commentary: posted",
	)
	if got := run("heartbeat", "claim", eventID, "--generation", generation); !strings.Contains(
		got,
		"status: acknowledged",
	) {
		t.Fatal(got)
	}
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(messageRoot)
	pending, err := messages.List(recipient)
	if err != nil || len(pending) != 0 {
		t.Fatalf("output receipt did not close publication: %v, %v", pending, err)
	}
	run("heartbeat", "check")
	if _, err := os.Stat(filepath.Join(root, "heartbeat.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy scheduling still active: %v", err)
	}
}

func heartbeatField(t *testing.T, output, name string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}
	t.Fatalf("missing %s in %s", name, output)
	return ""
}

type heartbeatQueueMode string

const (
	queueAccepted        heartbeatQueueMode = "accepted"
	queueUncertainFound  heartbeatQueueMode = "uncertain_found"
	queueUncertainAbsent heartbeatQueueMode = "uncertain_absent"
	queueUncertainError  heartbeatQueueMode = "uncertain_error"
)

func heartbeatQueueFixture(
	t *testing.T,
	root, messageRoot, thread string,
	modes ...heartbeatQueueMode,
) (string, *atomic.Int32) {
	t.Helper()
	directory, err := os.MkdirTemp("", "hb-queue-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "server.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var submissions atomic.Int32
	mode := queueAccepted
	if len(modes) > 0 {
		mode = modes[0]
	}
	var submittedClient atomic.Value
	submittedClient.Store("")
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			var request struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if wsjson.Read(ctx, connection, &request) != nil {
				return
			}
			if request.ID == 0 {
				continue
			}
			var result any = map[string]any{}
			switch request.Method {
			case "thread/read":
				result = map[string]any{
					"thread": map[string]any{
						"id":     thread,
						"cwd":    filepath.Dir(root),
						"status": map[string]string{"type": "idle"},
					},
				}
			case "thread/turns/list":
				result = map[string]any{"data": []any{map[string]string{"status": "completed"}}}
			case "thread/queue/add":
				data, err := os.ReadFile(filepath.Join(root, "supervisor.json"))
				var state heartbeat.State
				if err != nil || json.Unmarshal(data, &state) != nil || state.Pending == nil ||
					state.Pending.Attempt == nil ||
					state.Pending.Attempt.Phase != "submitting" {
					t.Errorf("submission preceded durable attempt: %s, %v", data, err)
				}
				var params map[string]any
				if err := json.Unmarshal(request.Params, &params); err != nil {
					t.Error(err)
				}
				input := params["input"].([]any)
				notification := input[0].(map[string]any)["text"].(string)
				if state.Pending != nil && state.Binding != nil {
					expected := heartbeat.Notification(
						"/fixture/concierge",
						root,
						messageRoot,
						"concierge",
						*state.Pending,
						state.Binding.Generation,
					)
					if notification != expected {
						t.Errorf("queue notification lost claim context: %s", notification)
					}
				}
				submissions.Add(1)
				submittedClient.Store(params["clientUserMessageId"].(string))
				if mode != queueAccepted {
					return
				}
				result = map[string]any{
					"queuedSubmission": map[string]any{
						"id":                  "native-queue-1",
						"clientUserMessageId": params["clientUserMessageId"],
						"input":               params["input"],
					},
				}
			case "thread/queue/list":
				if mode == queueUncertainFound {
					result = map[string]any{
						"data": []any{
							map[string]string{
								"clientUserMessageId": submittedClient.Load().(string),
							},
						},
					}
				}
				if mode == queueUncertainError {
					wsjson.Write(
						ctx,
						connection,
						map[string]any{
							"id": request.ID,
							"error": map[string]any{
								"code":    -32603,
								"message": "fixture lookup failure",
							},
						},
					)
					continue
				}
			}
			if wsjson.Write(
				ctx,
				connection,
				map[string]any{"id": request.ID, "result": result},
			) != nil {
				return
			}
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); listener.Close() })
	return path, &submissions
}

func TestHeartbeatUncertainDeliveryRetainsAndReconciles(t *testing.T) {
	if runUnderNativeParent(t) {
		return
	}
	for _, mode := range []heartbeatQueueMode{queueUncertainFound, queueUncertainAbsent, queueUncertainError} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			root := taskRoot(t, t.TempDir())
			messageRoot := t.TempDir()
			claimNativeParent(t, root)
			thread := "019a0000-0000-7000-8000-000000000001"
			t.Setenv("CODEX_THREAD_ID", thread)
			socket, count := heartbeatQueueFixture(t, root, messageRoot, thread, mode)
			invoke(
				t,
				root,
				"",
				"heartbeat",
				"register",
				"codex",
				"--socket",
				socket,
				"--thread",
				thread,
			)
			store := heartbeat.Store{Root: root}
			if err := store.Enable(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			state, err := store.Schedule(
				ctx,
				time.Now(),
				heartbeat.Observation{Active: true, Text: "working"},
				heartbeat.Settings{Interval: "1m", QuietLimit: 5},
			)
			if err != nil {
				t.Fatal(err)
			}
			eventID := state.Pending.ID
			recipient, _ := inbox.ParseRecipient("concierge")
			if err := store.Publish(ctx, messageRoot, recipient); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			state, err = store.Read(ctx)
			want := "uncertain"
			if mode == queueUncertainFound {
				want = "accepted"
			}
			if err != nil || state.Pending == nil || state.Pending.ID != eventID ||
				state.Pending.Attempt.Phase != want ||
				count.Load() != 1 {
				t.Fatalf(
					"uncertain delivery repeated or lost: %+v, count %d, error %v",
					state,
					count.Load(),
					err,
				)
			}
			if mode != queueUncertainAbsent {
				return
			}
			if _, err := store.Claim(ctx, eventID, time.Now().Add(-2*heartbeat.ClaimLease)); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if count.Load() != 2 {
				t.Fatalf(
					"expired observation reused for more than one uncertain attempt: %d",
					count.Load(),
				)
			}
			mutateHeartbeatRecord(t, root, func(state *heartbeat.State) {
				state.Pending.Claim = nil
				state.Pending.Attempt.Count = 3
				state.Pending.Attempt.Phase = "accepted"
				state.Pending.Attempt.At = time.Now().Add(-2 * heartbeat.ClaimLease)
			})
			if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err != nil {
				t.Fatal(err)
			}
			state, err = store.Read(ctx)
			if err != nil || state.Delivery != "retry_exhausted" || count.Load() != 2 {
				t.Fatalf("budget exceeded: %+v, count %d, error %v", state, count.Load(), err)
			}
			mutateHeartbeatRecord(
				t,
				root,
				func(state *heartbeat.State) { state.Binding.Owner.PID++ },
			)
			before, err := os.ReadFile(filepath.Join(root, "supervisor.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DeliverCodex(ctx, "/fixture/concierge", messageRoot, "concierge", time.Now()); err == nil {
				t.Fatal("stale binding admitted")
			}
			after, err := os.ReadFile(filepath.Join(root, "supervisor.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("stale delivery changed state: %v", err)
			}
		})
	}
}

func mutateHeartbeatRecord(t *testing.T, root string, change func(*heartbeat.State)) {
	t.Helper()
	path := filepath.Join(root, "supervisor.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state heartbeat.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	change(&state)
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
