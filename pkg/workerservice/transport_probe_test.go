package workerservice

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

func TestTransportProbeRoundTripDoesNotOpenTaskStores(t *testing.T) {
	parent, err := os.MkdirTemp("", "transport-probe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	directory := filepath.Join(parent, "task")
	grant, secret, _ := workeraccess.Issue(
		"probe-task",
		"probe-generation",
		workeraccess.Operations(),
	)
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	owner := remoteworker.SocketOwner{
		Task: grant.Task, Generation: grant.Generation, Verifier: grant.Verifier,
	}
	if err := remoteworker.ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: filepath.Join(directory, "worker.sock"), Net: "unix",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	root, messages := filepath.Join(parent, "state"), filepath.Join(parent, "messages")
	recipient, _ := inbox.ParseRecipient("primary")
	server, err := New(Config{Root: root, MessageRoot: messages, Recipient: recipient})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker service did not stop")
		}
	}()
	if err := remoteworker.ProbeSocket(ctx, directory, owner); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, messages, filepath.Join(parent, ".state.task-state-version.lock")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("transport probe opened a task store: %s %v", path, err)
		}
	}
}
