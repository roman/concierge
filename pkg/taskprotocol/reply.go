package taskprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/tincan/pkg/inbox"
)

var ErrInvalidReply = errors.New("message is not a task reply for this state root")

type Reply struct {
	Kind            string       `json:"kind"`
	State           string       `json:"state"`
	Task            string       `json:"task"`
	SpawnGeneration string       `json:"spawn_generation"`
	AssignmentID    string       `json:"assignment_id,omitempty"`
	ReplyKey        string       `json:"reply_key,omitempty"`
	Body            string       `json:"body"`
	Attachments     []Attachment `json:"attachments,omitempty"`
}

func DecodeReply(body, messageKey, taskRoot string) (Reply, error) {
	if len(body) > MaxReplyBytes {
		// Preserve large legacy text replies without decoding oversized file data.
		var envelope struct {
			Attachments emptyAttachments `json:"attachments"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			return Reply{}, ErrInvalidReply
		}
	}
	var reply Reply
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return Reply{}, fmt.Errorf("invalid task reply: %w", err)
	}
	if err := ValidateAttachments(reply.Attachments); err != nil {
		return Reply{}, err
	}
	if reply.Kind != "task-reply" || reply.State != filepath.Join(taskRoot, "tasks") ||
		strings.TrimSpace(reply.Body) == "" {
		return Reply{}, ErrInvalidReply
	}
	if _, err := taskmeta.ParseTaskID(reply.Task); err != nil {
		return Reply{}, err
	}
	if err := taskmeta.ValidateSpawnGeneration(reply.SpawnGeneration); err != nil {
		return Reply{}, err
	}
	if reply.AssignmentID != "" {
		if _, err := inbox.ParseMessageID(reply.AssignmentID); err != nil {
			return Reply{}, err
		}
		expectedKey := fmt.Sprintf("task-reply:%s:%s:%s:%s",
			taskRoot, reply.Task, reply.SpawnGeneration, reply.AssignmentID)
		if strings.TrimSpace(reply.ReplyKey) == "" || messageKey != expectedKey {
			return Reply{}, fmt.Errorf("%w: bound reply has an invalid key", ErrInvalidReply)
		}
	}
	return reply, nil
}

type emptyAttachments struct{}

func (*emptyAttachments) UnmarshalJSON(data []byte) error {
	// JSON invokes this for every occurrence, including duplicate field names.
	raw := bytes.TrimSpace(data)
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if len(raw) >= 2 && raw[0] == '[' && raw[len(raw)-1] == ']' &&
		len(bytes.TrimSpace(raw[1:len(raw)-1])) == 0 {
		return nil
	}
	return ErrInvalidReply
}
