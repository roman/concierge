package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

func runRemoteReply(t *testing.T, id, body string) (int, string) {
	t.Helper()
	var output bytes.Buffer
	code := Run(context.Background(), []string{"task", "reply", "send", "task-1",
		"--generation", "test-generation", "--key", "result", "--assignment-id", id, "--body-file", "-"},
		strings.NewReader(body), &output, &output)
	return code, output.String()
}

func TestOfflineRemoteReplyRetainsAttachmentBytes(t *testing.T) {
	remoteCLI(t, workerservice.Response{})
	secretFile := os.Getenv("CONCIERGE_WORKER_SECRET_FILE")
	socket := filepath.Join(filepath.Dir(secretFile), "offline.sock")
	attachment, err := taskprotocol.NewAttachment("evidence.bin", []byte{0, 128, 255})
	if err != nil {
		t.Fatal(err)
	}
	request := workerservice.Request{
		Task:        "task-1",
		Generation:  "test-generation",
		Operation:   workeraccess.SendReply,
		Key:         "result",
		MessageID:   "k-" + strings.Repeat("a", 64),
		Body:        "Done",
		Attachments: []taskprotocol.Attachment{attachment},
	}
	_, retained, err := sendRemoteReply(t.Context(), socket, secretFile, request)
	if err == nil || retained == "" {
		t.Fatalf("offline reply was not retained: %q %v", retained, err)
	}
	data, err := os.ReadFile(retained)
	if err != nil {
		t.Fatal(err)
	}
	record, err := replyoutbox.DecodeRecord(data)
	if err != nil || len(record.Payload.Attachments) != 1 ||
		!bytes.Equal(record.Payload.Attachments[0].Data, attachment.Data) {
		t.Fatalf("offline attachment lost: %v", err)
	}
}

func retainedRemoteReply(t *testing.T, id, body string) replyoutbox.Record {
	t.Helper()
	directory := filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE"))
	binding, err := replyoutbox.ReadContext(directory)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := replyoutbox.New(directory)
	path, err := store.Path(
		replyoutbox.Payload{Root: binding.Root, Task: "task-1", Generation: "test-generation",
			AssignmentID: id, Key: "result", Recipient: binding.Recipient, Body: body},
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("original reply was not retained: %v", err)
	}
	record, err := replyoutbox.DecodeRecord(data)
	if err != nil || record.Payload.Body != body || record.Payload.Generation != "test-generation" {
		t.Fatalf("retained reply differs: %+v %v", record.Progress, err)
	}
	return record
}

func TestRemoteReplyRetainsFullPayloadBeforeConnecting(t *testing.T) {
	remoteCLI(t, workerservice.Response{})
	t.Setenv(
		"CONCIERGE_WORKER_SOCKET",
		filepath.Join(filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE")), "offline.sock"),
	)
	id, body := "k-"+strings.Repeat("a", 64), "Completed offline\n"
	code, output := runRemoteReply(t, id, body)
	if code == 0 || !strings.Contains(output, "retained_reply:") {
		t.Fatalf("offline reply did not report retained input: code=%d %s", code, output)
	}
	if record := retainedRemoteReply(t, id, body); record.Complete() || record.Revoked {
		t.Fatal("offline reply was confirmed or revoked without a response")
	}
}

func TestRemoteReplyReusesCompleteReceiptsWhileOffline(t *testing.T) {
	id, body := "k-"+strings.Repeat("b", 64), "Completed once\n"
	requests := remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed,
		Reply: &workerservice.Reply{Root: "/primary/state", Recipient: "concierge", MessageID: id,
			Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed, Assignment: taskops.ReplyAcknowledged}})
	if code, output := runRemoteReply(t, id, body); code != 0 {
		t.Fatalf("first reply failed: %s", output)
	}
	<-requests
	if record := retainedRemoteReply(t, id, body); !record.Complete() {
		t.Fatal("complete confirmation was not persisted")
	}
	t.Setenv(
		"CONCIERGE_WORKER_SOCKET",
		filepath.Join(filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE")), "offline.sock"),
	)
	if code, output := runRemoteReply(t, id, body); code != 0 {
		t.Fatalf("confirmed reply required another connection: %s", output)
	}
}

func TestRemoteReplyStopsRetriesAfterAccessDenial(t *testing.T) {
	id, body := "k-"+strings.Repeat("c", 64), "Retain after revocation\n"
	requests := remoteCLI(
		t,
		workerservice.Response{
			Outcome: workerservice.Denied,
			Error:   workeraccess.ErrDenied.Error(),
		},
	)
	if code, output := runRemoteReply(t, id, body); code == 0 {
		t.Fatalf("denied reply reported success: %s", output)
	}
	<-requests
	if record := retainedRemoteReply(t, id, body); !record.Revoked {
		t.Fatal("access denial did not stop durable retries")
	}
	t.Setenv(
		"CONCIERGE_WORKER_SOCKET",
		filepath.Join(filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE")), "offline.sock"),
	)
	if code, output := runRemoteReply(t, id, body); code == 0 ||
		!strings.Contains(output, replyoutbox.ErrRevoked.Error()) {
		t.Fatalf("revoked reply attempted transport again: code=%d %s", code, output)
	}
}

func TestRemoteReceiveRequiresReplyContextBeforeAcceptingWork(t *testing.T) {
	remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed,
		Assignment: &workerservice.Assignment{
			Status: taskops.AssignmentCurrent,
			Body:   "Do the work",
		}})
	path := filepath.Join(
		filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE")),
		"reply-context.json",
	)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"task", "receive", "task-1", "k-" + strings.Repeat("a", 64),
			"--generation", "test-generation"},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code == 0 || !strings.Contains(output.String(), replyoutbox.ErrContext.Error()) {
		t.Fatalf("worker accepted work before reply context was ready: code=%d %s", code, &output)
	}
}

func TestRemoteReplyDoesNotClaimRetentionWhenStorageFails(t *testing.T) {
	remoteCLI(t, workerservice.Response{})
	path := filepath.Join(filepath.Dir(os.Getenv("CONCIERGE_WORKER_SECRET_FILE")), "outbox")
	if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	code, output := runRemoteReply(t, "k-"+strings.Repeat("a", 64), "Result")
	if code == 0 || !strings.Contains(output, replyoutbox.ErrStorage.Error()) ||
		strings.Contains(output, "retained_reply:") {
		t.Fatalf("unconfirmed storage claimed a retained reply: code=%d %s", code, output)
	}
}
