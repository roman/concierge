package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workerservice"
)

func TestReplyReconcileForwardsOnlyTheOriginalTupleToTheHost(t *testing.T) {
	root := shortTaskRoot(t)
	listener, err := net.ListenUnix(
		"unix",
		&net.UnixAddr{Name: filepath.Join(root, "host.sock"), Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	payload := replyoutbox.Payload{Root: root, Task: "task-1", Generation: "original-generation",
		AssignmentID: "k-" + strings.Repeat(
			"a",
			64,
		), Key: "original-key", Recipient: "concierge", Body: "Original result\n"}
	data, _ := json.Marshal(replyoutbox.Record{Format: 1, Payload: payload, Revoked: true})
	received := make(chan hostservice.Request, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		var request hostservice.Request
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		received <- request
		_ = json.NewEncoder(connection).Encode(hostservice.Response{Outcome: hostservice.Completed,
			Reconciled: &workerservice.Reply{Root: root, Recipient: payload.Recipient,
				MessageID: "k-" + strings.Repeat("b", 64), Publication: taskops.ReplyConfirmed,
				Archive: taskops.ReplyConfirmed, Assignment: taskops.ReplyAcknowledged}})
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	code := Run(
		ctx,
		[]string{"--root", root, "task", "reply", "reconcile", "--file", "-"},
		bytes.NewReader(data),
		&output,
		&output,
	)
	if code != 0 || !strings.Contains(output.String(), "generation: \"original-generation\"") {
		t.Fatalf("reconciliation command failed: code=%d %s", code, &output)
	}
	select {
	case request := <-received:
		if request.Operation != hostservice.ReconcileReply || request.Reconciliation == nil ||
			*request.Reconciliation != payload {
			t.Fatalf("reconciliation changed the original tuple: %+v", request)
		}
	case <-ctx.Done():
		t.Fatal("reconciliation never reached the primary host")
	}
}

func TestRemoteWorkerCannotReconcileARevokedReply(t *testing.T) {
	t.Setenv("CONCIERGE_WORKER_TASK", "task-1")
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"task", "reply", "reconcile", "--file", "-"},
		strings.NewReader("{}"),
		&output,
		&output,
	)
	if code == 0 ||
		!strings.Contains(output.String(), "remote workers may use only worker operations") {
		t.Fatalf("remote worker attempted primary reconciliation: code=%d %s", code, &output)
	}
}
