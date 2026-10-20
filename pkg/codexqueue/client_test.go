package codexqueue

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/roman/concierge/pkg/primary"
)

type dropResponse struct{}
type sameIDRequest struct{ receipt any }

func TestQueueAcceptanceAndUncertainty(t *testing.T) {
	for _, trial := range []struct {
		name                     string
		reply                    any
		wantUncertain, wantError bool
	}{
		{"accepted", map[string]any{"queuedSubmission": map[string]any{"id": "queued-1", "clientUserMessageId": "stable-event-generation", "input": []any{map[string]string{"type": "text", "text": "fixed notification"}}}}, false, false},
		{"empty receipt", map[string]any{}, true, true},
		{"wrong client", map[string]any{"queuedSubmission": map[string]string{"id": "queued-1", "clientUserMessageId": "other"}}, true, true},
		{"changed input", map[string]any{"queuedSubmission": map[string]any{"id": "queued-1", "clientUserMessageId": "stable-event-generation", "input": []any{map[string]string{"type": "text", "text": "altered"}}}}, true, true},
		{"colliding server request", sameIDRequest{receipt: map[string]any{"queuedSubmission": map[string]any{"id": "queued-1", "clientUserMessageId": "stable-event-generation", "input": []any{map[string]string{"type": "text", "text": "fixed notification"}}}}}, false, false},
		{"rejected", &rpcError{Code: -32001, Message: "overloaded"}, false, true},
		{"lost response", dropResponse{}, true, true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			path, token := queueFixture(t, func(method string, params json.RawMessage) any {
				if method != "thread/queue/add" {
					return map[string]any{}
				}
				var input struct {
					Thread   string `json:"threadId"`
					ClientID string `json:"clientUserMessageId"`
					Input    []struct {
						Text string `json:"text"`
					} `json:"input"`
				}
				if err := json.Unmarshal(params, &input); err != nil {
					t.Error(err)
				}
				if input.Thread != "selected" || input.ClientID != "stable-event-generation" ||
					len(input.Input) != 1 ||
					input.Input[0].Text != "fixed notification" {
					t.Errorf("wrong queue target: %s", params)
				}
				return trial.reply
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client, err := Open(ctx, path, token)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.Submit(ctx, "selected", "stable-event-generation", "fixed notification")
			if (err != nil) != trial.wantError ||
				errors.Is(err, ErrUncertain) != trial.wantUncertain {
				t.Fatalf("unexpected submission evidence: %v", err)
			}
		})
	}
}

func TestVerifiedThreadAndReadiness(t *testing.T) {
	for _, trial := range []struct {
		name, status, lastTurn string
		flags                  []string
		want                   Gate
	}{
		{"idle", "idle", "completed", nil, Ready},
		{"busy", "active", "inProgress", nil, Busy},
		{"permission", "active", "inProgress", []string{"waitingOnApproval"}, Permission},
		{"interrupted", "idle", "interrupted", nil, Interrupted},
		{"missing", "notLoaded", "completed", nil, Missing},
		{"unknown", "newState", "completed", nil, Unknown},
	} {
		t.Run(trial.name, func(t *testing.T) {
			path, token := queueFixture(t, func(method string, params json.RawMessage) any {
				switch method {
				case "thread/read":
					return map[string]any{
						"thread": map[string]any{
							"id":  "thread",
							"cwd": "/repo",
							"status": map[string]any{
								"type":        trial.status,
								"activeFlags": trial.flags,
							},
						},
					}
				case "thread/turns/list":
					return map[string]any{
						"data": []any{map[string]string{"status": trial.lastTurn}},
					}
				default:
					return map[string]any{}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client, err := Open(ctx, path, token)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			gate, err := client.Readiness(ctx, "thread", "/repo")
			if err != nil || gate != trial.want {
				t.Fatalf("gate %s, error %v", gate, err)
			}
		})
	}
}

func TestThreadAndRootMustMatchRegistration(t *testing.T) {
	path, token := queueFixture(t, func(string, json.RawMessage) any {
		return map[string]any{
			"thread": map[string]any{
				"id":     "thread",
				"cwd":    "/repo",
				"status": map[string]string{"type": "idle"},
			},
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Open(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, trial := range []struct{ thread, cwd string }{{"other", "/repo"}, {"thread", "/other"}} {
		if err := client.Verify(ctx, trial.thread, trial.cwd); err == nil {
			t.Fatalf("wrong destination accepted: %+v", trial)
		}
	}
}

func TestReplacementServerRejected(t *testing.T) {
	path, token := queueFixture(t, func(string, json.RawMessage) any { return map[string]any{} })
	token.StartTicks += "wrong"
	if client, err := Open(context.Background(), path, token); err == nil {
		client.Close()
		t.Fatal("replacement server accepted")
	}
}

func TestExplicitNativeSocketAliasRetainsPeerVerification(t *testing.T) {
	path, token := queueFixture(t, func(string, json.RawMessage) any { return map[string]any{} })
	alias := path + ".alias"
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	client, err := Open(context.Background(), alias, token)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	token.StartTicks += "other"
	if client, err := Open(context.Background(), alias, token); err == nil {
		client.Close()
		t.Fatal("alias bypassed peer verification")
	}
}

func TestReconcileUsesTheSelectedThreadAndClientID(t *testing.T) {
	path, token := queueFixture(t, func(method string, params json.RawMessage) any {
		if method != "thread/queue/list" {
			return map[string]any{}
		}
		var request struct {
			Thread string `json:"threadId"`
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			t.Error(err)
		}
		if request.Thread != "selected" {
			t.Errorf("wrong reconciliation thread: %s", params)
		}
		if request.Cursor == "" {
			return map[string]any{
				"data":       []any{map[string]string{"clientUserMessageId": "unrelated"}},
				"nextCursor": "next",
			}
		}
		return map[string]any{"data": []any{map[string]string{"clientUserMessageId": "stable"}}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Open(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, trial := range []struct {
		id   string
		want bool
	}{{"stable", true}, {"absent", false}} {
		found, err := client.Contains(ctx, "selected", trial.id)
		if err != nil || found != trial.want {
			t.Fatalf("reconcile %s: found %v, error %v", trial.id, found, err)
		}
	}
}

func TestReconcileFindsDispatchedInputInHistory(t *testing.T) {
	path, token := queueFixture(t, func(method string, params json.RawMessage) any {
		if method == "thread/items/list" {
			return map[string]any{
				"data": []any{
					map[string]any{
						"item": map[string]string{"type": "userMessage", "clientId": "dispatched"},
					},
				},
			}
		}
		return map[string]any{}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Open(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	found, err := client.Contains(ctx, "selected", "dispatched")
	if err != nil || !found {
		t.Fatalf("dispatched input lost: %v, %v", found, err)
	}
}

func queueFixture(
	t *testing.T,
	respond func(string, json.RawMessage) any,
) (string, primary.ProcessToken) {
	t.Helper()
	directory, err := os.MkdirTemp("", "cq-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "server.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
			result := respond(request.Method, request.Params)
			if collision, ok := result.(sameIDRequest); ok {
				if wsjson.Write(
					ctx,
					connection,
					map[string]any{
						"id":     request.ID,
						"method": "item/commandExecution/requestApproval",
						"params": map[string]any{},
					},
				) != nil {
					return
				}
				result = collision.receipt
			}
			if _, drop := result.(dropResponse); drop {
				return
			}
			response := map[string]any{"id": request.ID, "result": result}
			if failure, ok := result.(*rpcError); ok {
				response = map[string]any{"id": request.ID, "error": failure}
			}
			if wsjson.Write(
				ctx,
				connection,
				response,
			) != nil {
				return
			}
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); listener.Close() })
	token, err := primary.CaptureProcessToken(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return path, token
}
