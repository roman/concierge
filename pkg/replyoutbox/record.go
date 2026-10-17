package replyoutbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/roman/tincan/pkg/inbox"
)

const maxRecordBytes = maxPayloadBytes + 2048

type Progress struct {
	MessageID              string `json:"message_id,omitempty"`
	PublicationConfirmed   bool   `json:"publication_confirmed"`
	ArchiveConfirmed       bool   `json:"archive_confirmed"`
	AssignmentAcknowledged bool   `json:"assignment_acknowledged"`
}

type Record struct {
	Format   int      `json:"format"`
	Payload  Payload  `json:"payload"`
	Progress Progress `json:"progress"`
	Revoked  bool     `json:"revoked"`
}

func (record Record) Complete() bool {
	return record.Progress.PublicationConfirmed && record.Progress.ArchiveConfirmed &&
		(record.Payload.AssignmentID == "" || record.Progress.AssignmentAcknowledged)
}

func (record Record) validate() error {
	if record.Format != 1 || record.Payload.Validate() != nil {
		return ErrStorage
	}
	progress := record.Progress
	if progress.PublicationConfirmed {
		if _, err := inbox.ParseMessageID(progress.MessageID); err != nil {
			return ErrStorage
		}
	} else if progress.MessageID != "" || progress.ArchiveConfirmed || progress.AssignmentAcknowledged {
		return ErrStorage
	}
	if progress.AssignmentAcknowledged &&
		(!progress.ArchiveConfirmed || record.Payload.AssignmentID == "") {
		return ErrStorage
	}
	return nil
}

func DecodeRecord(data []byte) (Record, error) {
	if len(data) > maxRecordBytes {
		return Record{}, ErrStorage
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record Record
	if decoder.Decode(&record) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		record.validate() != nil {
		return Record{}, ErrStorage
	}
	return record, nil
}

func mergeProgress(record Record, incoming Progress) (Record, error) {
	current := record.Progress
	if incoming.MessageID != "" {
		if current.MessageID != "" && current.MessageID != incoming.MessageID {
			return Record{}, ErrConflict
		}
		current.MessageID = incoming.MessageID
	}
	current.PublicationConfirmed = current.PublicationConfirmed || incoming.PublicationConfirmed
	current.ArchiveConfirmed = current.ArchiveConfirmed || incoming.ArchiveConfirmed
	current.AssignmentAcknowledged = current.AssignmentAcknowledged ||
		incoming.AssignmentAcknowledged
	record.Progress = current
	if err := record.validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}
