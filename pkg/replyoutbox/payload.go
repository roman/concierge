// Package replyoutbox retains the exact original reply until delivery is confirmed.
package replyoutbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/tincan/pkg/inbox"
)

const maxPayloadBytes = 2 << 20

var ErrInvalidPayload = errors.New("reply payload is incomplete, invalid or oversized")

// Payload contains the complete original tuple. Root and recipient are binding
// metadata, never resources selected by a remote worker request.
type Payload struct {
	Root         string                    `json:"root"`
	Task         string                    `json:"task"`
	Generation   string                    `json:"generation"`
	AssignmentID string                    `json:"assignment_id"`
	Key          string                    `json:"key"`
	Recipient    string                    `json:"recipient"`
	Body         string                    `json:"body"`
	Attachments  []taskprotocol.Attachment `json:"attachments,omitempty"`
}

func (payload Payload) Validate() error {
	_, err := payload.Encode()
	return err
}

func (payload Payload) Encode() ([]byte, error) {
	if err := taskprotocol.ValidateAttachments(payload.Attachments); err != nil {
		return nil, ErrInvalidPayload
	}
	for _, value := range []string{payload.Root, payload.Task, payload.Generation,
		payload.AssignmentID, payload.Key, payload.Recipient, payload.Body} {
		if !utf8.ValidString(value) {
			return nil, ErrInvalidPayload
		}
	}
	if _, err := taskmeta.ParseAbsolutePath(payload.Root); err != nil {
		return nil, ErrInvalidPayload
	}
	if _, err := taskmeta.ParseTaskID(payload.Task); err != nil {
		return nil, ErrInvalidPayload
	}
	if taskmeta.ValidateSpawnGeneration(payload.Generation) != nil ||
		strings.TrimSpace(payload.Key) == "" || len(payload.Key) > 1024 ||
		strings.TrimSpace(payload.Body) == "" || len(payload.Body) > 1<<20 {
		return nil, ErrInvalidPayload
	}
	recipient, err := inbox.ParseRecipient(payload.Recipient)
	if err != nil || recipient.String() != payload.Recipient {
		return nil, ErrInvalidPayload
	}
	if payload.AssignmentID != "" {
		if _, err := inbox.ParseMessageID(payload.AssignmentID); err != nil {
			return nil, ErrInvalidPayload
		}
	}
	payload.Attachments = taskprotocol.CanonicalAttachments(payload.Attachments)
	data, err := json.Marshal(payload)
	if err != nil || len(data) > maxPayloadBytes {
		return nil, ErrInvalidPayload
	}
	return data, nil
}

func DecodePayload(data []byte) (Payload, error) {
	if len(data) > maxPayloadBytes {
		return Payload{}, ErrInvalidPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload Payload
	if decoder.Decode(&payload) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		payload.Validate() != nil {
		return Payload{}, ErrInvalidPayload
	}
	return payload, nil
}

func (payload Payload) ID() (string, error) {
	if err := payload.Validate(); err != nil {
		return "", err
	}
	// Primary publication deduplicates bound replies by assignment. Changing
	// a body or user key must conflict with that entry rather than create one.
	payload.Body = ""
	payload.Attachments = nil
	if payload.AssignmentID != "" {
		payload.Key = ""
	}
	identity, _ := json.Marshal(payload)
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}

func (payload Payload) Equal(other Payload) bool {
	left, leftError := payload.Encode()
	right, rightError := other.Encode()
	return leftError == nil && rightError == nil && bytes.Equal(left, right)
}
