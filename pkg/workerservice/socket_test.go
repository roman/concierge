package workerservice

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/workeraccess"
)

func socketFixture(t *testing.T, configure func(*Server)) (string, Request, context.CancelFunc) {
	t.Helper()
	server, _, _, request := dispatchFixture(t)
	if configure != nil {
		configure(server)
	}
	path, _, cancel := serveSocketFixture(t, server, "")
	return path, request, cancel
}

func serveSocketFixture(
	t *testing.T,
	server *Server,
	task string,
) (string, Request, context.CancelFunc) {
	t.Helper()
	directory, err := os.MkdirTemp("", "worker-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		if task != "" {
			done <- server.ServeTask(ctx, listener, task)
			return
		}
		done <- server.Serve(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("worker server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker socket did not stop after cancellation")
		}
	})
	return path, Request{}, cancel
}

func TestTaskSocketKeepsNoisyTaskAdmissionSeparate(t *testing.T) {
	server, _, _, request := dispatchFixture(t)
	noisy, _, _ := serveSocketFixture(t, server, "noisy")
	quiet, _, _ := serveSocketFixture(t, server, request.Task)
	request.Operation, request.MessageID = workeraccess.ListPending, ""
	data, _ := json.Marshal(request)
	for range workerBurst {
		if response := socketExchange(t, noisy, data); response.Outcome != Denied {
			t.Fatalf("socket accepted another task: %+v", response)
		}
	}
	connection, err := net.Dial("unix", noisy)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	var response Response
	if err := decodeBounded(connection, &response); err != nil || response.Outcome != Busy {
		t.Fatalf("rate-rejected socket waited for a request: %+v %v", response, err)
	}
	if response := socketExchange(t, quiet, data); response.Outcome != Completed {
		t.Fatalf("noisy task starved unrelated worker: %+v", response)
	}
}

func TestTaskSocketBoundsUnfinishedConnections(t *testing.T) {
	server, _, _, request := dispatchFixture(t)
	noisy, _, _ := serveSocketFixture(t, server, "noisy")
	quiet, _, _ := serveSocketFixture(t, server, request.Task)
	for range 2 {
		connection, err := net.Dial("unix", noisy)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		if _, err := connection.Write([]byte(`{"task":`)); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := net.Dial("unix", noisy)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	var byte [1]byte
	if _, err := connection.Read(byte[:]); err == nil {
		t.Fatal("task exceeded its connection limit")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("task retained excess unfinished connection")
	}
	request.Operation, request.MessageID = workeraccess.ListPending, ""
	data, _ := json.Marshal(request)
	if response := socketExchange(t, quiet, data); response.Outcome != Completed {
		t.Fatalf("unfinished noisy requests blocked another task: %+v", response)
	}
}

func socketExchange(t *testing.T, path string, data []byte) Response {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decodeBounded(connection, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestWorkerSocketAcceptsOnlyBoundWorkerRequests(t *testing.T) {
	path, request, _ := socketFixture(t, nil)
	data, _ := json.Marshal(request)
	response := socketExchange(t, path, data)
	if response.Outcome != Completed || response.Assignment == nil ||
		response.Assignment.Body != "Complete the work" {
		t.Fatalf("bound request: %+v", response)
	}
	for _, body := range []string{
		`{"operation":"land","task":"task-1"}`,
		strings.TrimSuffix(string(data), "}") + `,"root":"/other"}`,
		strings.TrimSuffix(string(data), "}") + `,"recipient":"other"}`,
		strings.TrimSuffix(string(data), "}") + `,"body_file":"/remote/result"}`,
		string(data) + `{}`,
	} {
		response := socketExchange(t, path, []byte(body))
		if response.Outcome != Invalid || response.Assignment != nil ||
			strings.Contains(response.Error, request.Secret) {
			t.Fatalf("invalid wire request: %+v", response)
		}
	}
}

func TestWorkerSocketRateLimitIsBoundedAndRecovers(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var timestamp atomic.Int64
	timestamp.Store(now.UnixNano())
	path, request, _ := socketFixture(t, func(server *Server) {
		server.admission.now = func() time.Time { return time.Unix(0, timestamp.Load()) }
	})
	request.Operation, request.MessageID = workeraccess.ListPending, ""
	data, _ := json.Marshal(request)
	for range workerBurst {
		if response := socketExchange(t, path, data); response.Outcome != Completed {
			t.Fatalf("request below rate limit: %+v", response)
		}
	}
	if response := socketExchange(t, path, data); response.Outcome != Busy {
		t.Fatalf("rate limit did not reject excess requests: %+v", response)
	}
	timestamp.Store(now.Add(time.Second).UnixNano())
	if response := socketExchange(t, path, data); response.Outcome != Completed {
		t.Fatalf("rate limit did not recover: %+v", response)
	}
}

func TestWorkerSocketCancellationClosesAnUnfinishedRequest(t *testing.T) {
	path, _, cancel := socketFixture(t, nil)
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if _, err := connection.Write([]byte(`{"task":`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	var data [1]byte
	_, err = connection.Read(data[:])
	if timeout, ok := err.(net.Error); err == nil || (ok && timeout.Timeout()) {
		t.Fatalf("cancelled server left the unfinished request open: %v", err)
	}
}

func TestWorkerSocketBoundsAnUnfinishedRead(t *testing.T) {
	path, _, _ := socketFixture(t, nil)
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(workerIOTimeout + 2*time.Second))
	if _, err := connection.Write([]byte(`{"task":`)); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decodeBounded(connection, &response); err != nil || response.Outcome != Invalid {
		t.Fatalf("unfinished read exceeded its deadline: %+v %v", response, err)
	}
}
