// Package codexqueue targets one explicitly registered native app-server.
package codexqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/roman/concierge/pkg/primary"
)

var ErrUncertain = errors.New("native queue submission outcome is uncertain")

type Gate string

const (
	Ready       Gate = "ready"
	Busy        Gate = "busy"
	Permission  Gate = "permission_blocked"
	Interrupted Gate = "interrupted"
	Missing     Gate = "missing"
	Unknown     Gate = "unknown"
)

type Client struct {
	Server     primary.ProcessToken
	connection *websocket.Conn
	sequence   int
}

// Open never discovers or starts a daemon. The kernel peer token fences an
// endpoint replacement even when its socket path is reused.
func Open(ctx context.Context, path string, expected primary.ProcessToken) (*Client, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("Codex requires an explicit clean Unix socket path")
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("Codex endpoint must be a Unix socket")
	}
	var actual primary.ProcessToken
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
			if err != nil {
				return nil, err
			}
			peer, err := primary.PeerCaller(connection.(*net.UnixConn))
			if err == nil && peer.UID != uint32(os.Geteuid()) {
				err = errors.New("Codex endpoint belongs to another user")
			}
			if err == nil {
				actual, err = primary.CaptureProcessToken(peer.PID)
			}
			if err == nil && expected != (primary.ProcessToken{}) && actual != expected {
				err = errors.New(
					"Codex endpoint process changed; register the legitimate destination again",
				)
			}
			if err != nil {
				connection.Close()
				return nil, err
			}
			return connection, nil
		},
	}
	connection, _, err := websocket.Dial(
		ctx,
		"ws://localhost/",
		&websocket.DialOptions{
			HTTPClient: &http.Client{
				Transport:     transport,
				CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Codex endpoint redirect rejected") },
			},
		},
	)
	if err != nil {
		return nil, err
	}
	connection.SetReadLimit(2 << 20)
	client := &Client{Server: actual, connection: connection}
	if err := client.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "concierge", "version": "0.1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		connection.CloseNow()
		return nil, err
	}
	if err := wsjson.Write(ctx, connection, map[string]string{"method": "initialized"}); err != nil {
		connection.CloseNow()
		return nil, err
	}
	return client, nil
}

func (client *Client) Close() { client.connection.CloseNow() }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (failure *rpcError) Error() string {
	return fmt.Sprintf("Codex RPC %d: %s", failure.Code, failure.Message)
}

func (client *Client) call(ctx context.Context, method string, params any, result any) error {
	client.sequence++
	id := client.sequence
	if err := wsjson.Write(ctx, client.connection, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var response struct {
			Method string          `json:"method"`
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := wsjson.Read(ctx, client.connection, &response); err != nil {
			return err
		}
		if response.ID != id || response.Method != "" {
			continue
		}
		if response.Error != nil {
			return response.Error
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	}
}

type thread struct {
	ID     string `json:"id"`
	Cwd    string `json:"cwd"`
	Status struct {
		Type        string   `json:"type"`
		ActiveFlags []string `json:"activeFlags"`
	} `json:"status"`
}

func (client *Client) readThread(ctx context.Context, id, cwd string) (thread, error) {
	var result struct {
		Thread thread `json:"thread"`
	}
	if err := client.call(ctx, "thread/read", map[string]string{"threadId": id}, &result); err != nil {
		return thread{}, err
	}
	if result.Thread.ID != id || result.Thread.Cwd != cwd {
		return thread{}, errors.New(
			"Codex thread UUID or working directory differs from the registered primary",
		)
	}
	return result.Thread, nil
}

func (client *Client) Verify(ctx context.Context, id, cwd string) error {
	thread, err := client.readThread(ctx, id, cwd)
	if err != nil {
		return err
	}
	if thread.Status.Type == "notLoaded" || thread.Status.Type == "" {
		return errors.New(
			"Codex primary thread is not loaded; resume it explicitly before registration",
		)
	}
	return nil
}

func (client *Client) Readiness(ctx context.Context, id, cwd string) (Gate, error) {
	thread, err := client.readThread(ctx, id, cwd)
	if err != nil {
		return Unknown, err
	}
	switch thread.Status.Type {
	case "notLoaded":
		return Missing, nil
	case "active":
		if slices.Contains(thread.Status.ActiveFlags, "waitingOnApproval") {
			return Permission, nil
		}
		return Busy, nil
	case "idle":
	default:
		return Unknown, nil
	}
	var turns struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := client.call(ctx, "thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded"}, &turns); err != nil {
		return Unknown, err
	}
	if len(turns.Data) > 0 {
		switch turns.Data[0].Status {
		case "interrupted":
			return Interrupted, nil
		case "completed":
		default:
			return Unknown, nil
		}
	}
	return Ready, nil
}

// Submit does not deduplicate client IDs. Every ambiguous write or response is
// retained as uncertain by the host rather than retried through another route.
func (client *Client) Submit(ctx context.Context, threadID, clientID, text string) error {
	var result struct {
		Queued struct {
			ID       string `json:"id"`
			ClientID string `json:"clientUserMessageId"`
			Input    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"input"`
		} `json:"queuedSubmission"`
	}
	err := client.call(
		ctx,
		"thread/queue/add",
		map[string]any{
			"threadId":            threadID,
			"clientUserMessageId": clientID,
			"input": []map[string]any{
				{"type": "text", "text": text, "text_elements": []any{}},
			},
		},
		&result,
	)
	var rejected *rpcError
	if err != nil && !errors.As(err, &rejected) {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	if err == nil &&
		(result.Queued.ID == "" || result.Queued.ClientID != clientID || len(result.Queued.Input) != 1 || result.Queued.Input[0].Type != "text" || result.Queued.Input[0].Text != text) {
		return fmt.Errorf("%w: native queue receipt differs from the submitted event", ErrUncertain)
	}
	return err
}

func (client *Client) Contains(ctx context.Context, threadID, clientID string) (bool, error) {
	cursor := ""
	for range 32 {
		params := map[string]any{"threadId": threadID, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ClientID string `json:"clientUserMessageId"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		if err := client.call(ctx, "thread/queue/list", params, &page); err != nil {
			return false, err
		}
		for _, item := range page.Data {
			if item.ClientID == clientID {
				return true, nil
			}
		}
		if page.Next == "" {
			return client.containsHistory(ctx, threadID, clientID)
		}
		cursor = page.Next
	}
	return false, errors.New("Codex queue reconciliation exceeded 3200 entries")
}

// Absence from both bounded reads is not proof that an uncertain submission
// failed. The host retains that uncertainty instead of blindly inserting again.
func (client *Client) containsHistory(
	ctx context.Context,
	threadID, clientID string,
) (bool, error) {
	cursor := ""
	for range 32 {
		params := map[string]any{"threadId": threadID, "limit": 100, "sortDirection": "desc"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				Item struct {
					Type     string `json:"type"`
					ClientID string `json:"clientId"`
				} `json:"item"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		if err := client.call(ctx, "thread/items/list", params, &page); err != nil {
			return false, err
		}
		for _, entry := range page.Data {
			if entry.Item.Type == "userMessage" && entry.Item.ClientID == clientID {
				return true, nil
			}
		}
		if page.Next == "" {
			return false, nil
		}
		cursor = page.Next
	}
	return false, errors.New("Codex history reconciliation exceeded 3200 entries")
}
