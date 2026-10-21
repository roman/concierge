package replyoutbox

import (
	"errors"
	"os"
	"testing"

	"github.com/roman/concierge/pkg/taskprotocol"
)

func TestOutboxRetainsAttachmentsAndRejectsReplacement(t *testing.T) {
	_, store := privateOutbox(t)
	payload := payloadFixture()
	attachment, _ := taskprotocol.NewAttachment("evidence.bin", []byte{0, 255, 128})
	payload.Attachments = []taskprotocol.Attachment{attachment}
	if _, err := store.Stage(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path(payload)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := DecodeRecord(data)
	if err != nil || !record.Payload.Equal(payload) {
		t.Fatalf("outbox changed attachment: %v", err)
	}
	for _, bound := range []string{payload.AssignmentID, ""} {
		original := payload
		original.AssignmentID = bound
		if _, err := store.Stage(t.Context(), original); err != nil {
			t.Fatal(err)
		}
		for _, replacement := range []struct{ name, body string }{
			{"other.bin", string(attachment.Data)}, {attachment.Name, "changed"},
		} {
			changed := original
			other, _ := taskprotocol.NewAttachment(replacement.name, []byte(replacement.body))
			changed.Attachments = []taskprotocol.Attachment{other}
			if _, err := store.Stage(t.Context(), changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed attachments escaped original identity: %v", err)
			}
		}
	}
}

func TestOutboxRetryIgnoresAttachmentOrder(t *testing.T) {
	_, store := privateOutbox(t)
	payload := payloadFixture()
	first, _ := taskprotocol.NewAttachment("first.txt", []byte("first"))
	second, _ := taskprotocol.NewAttachment("second.txt", []byte("second"))
	payload.Attachments = []taskprotocol.Attachment{first, second}
	if _, err := store.Stage(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	payload.Attachments = []taskprotocol.Attachment{second, first}
	if _, err := store.Stage(t.Context(), payload); err != nil {
		t.Fatalf("reordered retry rejected: %v", err)
	}
}
