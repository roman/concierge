package hostservice

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type shutdownErrorConnection struct {
	*net.UnixConn
	shutdownError error
}

func (connection shutdownErrorConnection) CloseWrite() error {
	// Linux permits this shutdown after peer closure. Inject the Darwin error
	// after the real shutdown so both platforms exercise the same recovery.
	_ = connection.UnixConn.CloseWrite()
	return &net.OpError{
		Op: "close", Net: "unix",
		Err: os.NewSyscallError("shutdown", connection.shutdownError),
	}
}

func TestReceiveCallResponseAfterPeerCloses(t *testing.T) {
	request := intakeRequest()
	completed, err := json.Marshal(Response{
		Outcome: Completed,
		Task:    &TaskResult{Definition: *request.Intake, State: "queued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		body          string
		shutdownError syscall.Errno
		outcome       Outcome
	}{
		{name: "disconnected", body: string(completed), shutdownError: syscall.ENOTCONN, outcome: Completed},
		{name: "broken pipe", body: string(completed), shutdownError: syscall.EPIPE, outcome: Completed},
		{name: "invalid response", body: `{"outcome":"completed"}`, shutdownError: syscall.ENOTCONN},
		{name: "unrelated shutdown failure", body: string(completed), shutdownError: syscall.EBADF},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := closedPeerResponse(t, test.body)
			response, err := receiveCallResponse(shutdownErrorConnection{
				UnixConn: connection, shutdownError: test.shutdownError,
			}, request)
			if test.outcome == "" {
				if !errors.Is(err, ErrOutcomeUnknown) {
					t.Fatalf("response %+v, error %v; want unknown outcome", response, err)
				}
				return
			}
			if err != nil || response.Outcome != test.outcome {
				t.Fatalf("response %+v, error %v; want %s", response, err, test.outcome)
			}
		})
	}
}

func closedPeerResponse(t *testing.T, body string) *net.UnixConn {
	t.Helper()
	directory, err := os.MkdirTemp("", "host-shutdown-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	address := &net.UnixAddr{Name: filepath.Join(directory, "host.sock"), Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connection, err := net.DialUnix("unix", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := io.WriteString(peer, body); err != nil {
		t.Fatal(err)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	return connection
}
