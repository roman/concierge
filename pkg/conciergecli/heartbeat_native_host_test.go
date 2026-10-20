package conciergecli

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/tincan/pkg/inbox"
)

func nativeStartHost(t *testing.T, ctx context.Context, root, messages, concierge string) {
	t.Helper()
	recipient, _ := inbox.ParseRecipient("concierge")
	server, err := hostservice.New(
		hostservice.Config{
			Root:            root,
			MessageRoot:     messages,
			Recipient:       recipient,
			HerdrExecutable: "/bin/false",
			WorkerCommands:  taskops.CommandPaths{Concierge: concierge, Tincan: concierge},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hostCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- server.ServeReady(hostCtx, listener.UnixListener, func() error { close(ready); return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		listener.Close()
		t.Fatalf("private host failed: %v", err)
	case <-ctx.Done():
		cancel()
		listener.Close()
		t.Fatal(ctx.Err())
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("private host did not stop")
		}
		listener.Close()
	})
}

func nativeSendWork(t *testing.T, messages string) string {
	t.Helper()
	box, err := inbox.New(messages)
	if err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	message, err := box.Send(
		recipient,
		"native-fixture-work",
		"Disposable supervisor acceptance work",
	)
	if err != nil {
		t.Fatal(err)
	}
	return message.ID.String()
}

func nativeProject(t *testing.T) string {
	t.Helper()
	project, err := os.MkdirTemp("/tmp", "hb-claude-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })
	return project
}
