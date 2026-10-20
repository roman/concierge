package codexqueue

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLegacyHistoryRechecksThreadBeforeReadiness(t *testing.T) {
	for _, trial := range []struct {
		name, status, turn string
		want               Gate
	}{
		{"completed", "idle", "completed", Ready},
		{"interrupted", "idle", "interrupted", Interrupted},
		{"became busy", "active", "inProgress", Busy},
		{"unknown turn", "idle", "unknown", Unknown},
	} {
		t.Run(trial.name, func(t *testing.T) {
			path, token := queueFixture(t, func(method string, params json.RawMessage) any {
				if method == "thread/turns/list" || method == "thread/items/list" {
					return &rpcError{Code: -32601, Message: "not supported yet"}
				}
				var input struct {
					Include bool `json:"includeTurns"`
				}
				json.Unmarshal(params, &input)
				status := "idle"
				if input.Include {
					status = trial.status
				}
				return map[string]any{
					"thread": map[string]any{
						"id":     "thread",
						"cwd":    "/repo",
						"status": map[string]string{"type": status},
						"turns": []any{
							map[string]any{
								"status": trial.turn,
								"items": []any{
									map[string]string{
										"type":     "userMessage",
										"clientId": "dispatched",
									},
								},
							},
						},
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
			if gate, err := client.Readiness(ctx, "thread", "/repo"); err != nil ||
				gate != trial.want {
				t.Fatalf("gate %s, %v", gate, err)
			}
		})
	}
}

func TestLegacyHistoryReconciliationAndUnavailableReads(t *testing.T) {
	for _, trial := range []struct {
		name                 string
		code                 int
		turns                any
		wantFound, wantError bool
	}{
		{"matching input", -32601, []any{map[string]any{"status": "completed", "items": []any{map[string]string{"type": "userMessage", "clientId": "dispatched"}}}}, true, false},
		{"absent input", -32601, []any{}, false, false},
		{"null turns", -32601, nil, false, true},
		{"RPC failure", -32603, []any{}, false, true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			path, token := queueFixture(t, func(method string, params json.RawMessage) any {
				switch method {
				case "thread/items/list", "thread/turns/list":
					return &rpcError{Code: trial.code, Message: "fixture error"}
				case "thread/read":
					var input struct {
						Include bool `json:"includeTurns"`
					}
					json.Unmarshal(params, &input)
					if trial.code != -32601 && input.Include {
						t.Error("generic RPC failure used history fallback")
					}
					return map[string]any{
						"thread": map[string]any{
							"id":     "thread",
							"cwd":    "/repo",
							"status": map[string]string{"type": "idle"},
							"turns":  trial.turns,
						},
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
			if found, err := client.Contains(ctx, "thread", "dispatched"); found != trial.wantFound ||
				(err != nil) != trial.wantError {
				t.Fatalf("reconcile: %t, %v", found, err)
			}
			if trial.wantError {
				if gate, err := client.Readiness(ctx, "thread", "/repo"); gate != Unknown ||
					err == nil {
					t.Fatalf("unverified readiness: %s, %v", gate, err)
				}
			}
		})
	}
}

func TestLegacyHistoryRequiresTurnField(t *testing.T) {
	path, token := queueFixture(t, func(method string, params json.RawMessage) any {
		if method == "thread/turns/list" {
			return &rpcError{Code: -32601, Message: "unsupported"}
		}
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
	if gate, err := client.Readiness(ctx, "thread", "/repo"); gate != Unknown || err == nil {
		t.Fatalf("omitted turns permitted readiness: %s, %v", gate, err)
	}
}
