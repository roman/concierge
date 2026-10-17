package remoteworker

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
)

func TestSocketProbeRequiresWorkerProtocolRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		want     error
	}{
		{name: "worker service", response: `{"outcome":"invalid","error":"invalid worker request"}`},
		{name: "rate limited worker service", response: `{"outcome":"busy","error":"worker request rate limit reached; retry later"}`},
		{name: "broken forward", want: ErrTunnelUnreachable},
		{name: "wrong service", response: `{"status":"ready"}`, want: ErrTunnelUnreachable},
		{name: "unexpected fields", response: `{"outcome":"invalid","error":"invalid worker request","reply":{}}`, want: ErrTunnelUnreachable},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory, owner := socketOwnerFixture(t)
			if err := ClaimSocketDirectory(directory, owner); err != nil {
				t.Fatal(err)
			}
			listener, err := net.ListenUnix("unix", &net.UnixAddr{
				Name: filepath.Join(directory, "worker.sock"), Net: "unix",
			})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan string, 1)
			go func() {
				connection, err := listener.AcceptUnix()
				if err != nil {
					done <- err.Error()
					return
				}
				defer connection.Close()
				request, _ := io.ReadAll(connection)
				_, _ = io.WriteString(connection, test.response)
				done <- string(request)
			}()
			err = ProbeSocket(context.Background(), directory, owner)
			if !errors.Is(err, test.want) {
				t.Fatalf("probe error=%v, want %v", err, test.want)
			}
			if request := <-done; request != "{}" {
				t.Fatalf("probe sent a worker operation or credential: %q", request)
			}
		})
	}
}

func TestSocketProbeRejectsUnknownOwnershipBeforeConnecting(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ProbeSocket(context.Background(), directory, owner); !errors.Is(
		err,
		ErrSocketOwnership,
	) {
		t.Fatalf("unknown socket was probed: %v", err)
	}
}
