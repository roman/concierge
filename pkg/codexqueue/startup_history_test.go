package codexqueue

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnavailableStartupHistoryCanRecover(t *testing.T) {
	var materialized atomic.Bool
	path, token := queueFixture(t, func(method string, params json.RawMessage) any {
		switch method {
		case "thread/queue/list":
			return map[string]any{"data": []any{}}
		case "thread/turns/list", "thread/items/list":
			if !materialized.Load() {
				return &rpcError{Code: -32601, Message: "list_turns is not supported yet"}
			}
			if method == "thread/turns/list" {
				return map[string]any{"data": []any{map[string]string{"status": "completed"}}}
			}
			return map[string]any{"data": []any{map[string]any{
				"item": map[string]string{"type": "userMessage", "clientId": "dispatched"},
			}}}
		case "thread/read":
			var input struct {
				Include bool `json:"includeTurns"`
			}
			if err := json.Unmarshal(params, &input); err != nil {
				t.Error(err)
			}
			if input.Include {
				return &rpcError{Code: -32601, Message: "list_turns is not supported yet"}
			}
			return map[string]any{"thread": map[string]any{
				"id": "thread", "cwd": "/repo", "status": map[string]string{"type": "idle"},
			}}
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
	for _, trial := range []struct {
		name         string
		materialized bool
		want         Gate
		wantError    bool
	}{
		{"startup unavailable", false, Unknown, true},
		{"execution materialized", true, Ready, false},
	} {
		t.Run(trial.name, func(t *testing.T) {
			materialized.Store(trial.materialized)
			gate, err := client.Readiness(ctx, "thread", "/repo")
			if gate != trial.want || (err != nil) != trial.wantError {
				t.Fatalf("readiness: %s, %v", gate, err)
			}
			found, err := client.Contains(ctx, "thread", "dispatched")
			if found != trial.materialized || (err != nil) != trial.wantError {
				t.Fatalf("reconciliation: %t, %v", found, err)
			}
		})
	}
}
