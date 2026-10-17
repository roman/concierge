// Package workerservice serves task-scoped operations on a separate Unix socket.
package workerservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

const maxWireBytes = 2 << 20
const maxBodyBytes = 1 << 20

var ErrInvalidRequest = errors.New("invalid worker request")

// Request carries content and message IDs. The host selects all resources.
type Request struct {
	Task       string                 `json:"task"`
	Generation string                 `json:"generation"`
	Secret     string                 `json:"secret"`
	Operation  workeraccess.Operation `json:"operation"`
	MessageID  string                 `json:"message_id,omitempty"`
	Key        string                 `json:"key,omitempty"`
	Body       string                 `json:"body,omitempty"`
}

func (request Request) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[worker request redacted]")
}

func (request Request) validate() error {
	if _, err := taskmeta.ParseTaskID(request.Task); err != nil {
		return ErrInvalidRequest
	}
	if taskmeta.ValidateSpawnGeneration(request.Generation) != nil ||
		len(request.Secret) == 0 || len(request.Secret) > 256 ||
		!slices.Contains(workeraccess.Operations(), request.Operation) {
		return ErrInvalidRequest
	}
	if request.MessageID != "" {
		if _, err := inbox.ParseMessageID(request.MessageID); err != nil {
			return ErrInvalidRequest
		}
	}
	switch request.Operation {
	case workeraccess.SendReply:
		if strings.TrimSpace(request.Key) == "" || len(request.Key) > 1024 ||
			strings.TrimSpace(request.Body) == "" || len(request.Body) > maxBodyBytes {
			return ErrInvalidRequest
		}
		return nil
	case workeraccess.ListPending, workeraccess.ReadSettings:
		if request.MessageID != "" {
			return ErrInvalidRequest
		}
	default:
		if request.MessageID == "" {
			return ErrInvalidRequest
		}
	}
	if request.Key != "" || request.Body != "" {
		return ErrInvalidRequest
	}
	return nil
}

type Outcome string

const (
	Completed Outcome = "completed"
	Denied    Outcome = "denied"
	Invalid   Outcome = "invalid"
	Failed    Outcome = "failed"
	Uncertain Outcome = "uncertain"
	Busy      Outcome = "busy"
)

type Assignment struct {
	Status taskops.AssignmentState `json:"status"`
	Body   string                  `json:"body,omitempty"`
}

type Reply struct {
	MessageID  string                       `json:"message_id"`
	Assignment taskops.ReplyAcknowledgement `json:"assignment"`
}

type Response struct {
	Outcome    Outcome                  `json:"outcome"`
	Error      string                   `json:"error,omitempty"`
	Pending    []taskops.PendingMessage `json:"pending,omitempty"`
	Bootstrap  *taskops.BootstrapInfo   `json:"bootstrap,omitempty"`
	Settings   *taskops.WorkerSettings  `json:"settings,omitempty"`
	Assignment *Assignment              `json:"assignment,omitempty"`
	Reply      *Reply                   `json:"reply,omitempty"`
}

func decodeBounded(reader io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxWireBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxWireBytes {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidRequest
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}
