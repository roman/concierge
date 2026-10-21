package taskprotocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	MaxAttachments          = 8
	MaxAttachmentBytes      = 256 << 10
	MaxTotalAttachmentBytes = 512 << 10
	MaxReplyBytes           = 2 << 20
)

var ErrInvalidAttachment = errors.New("invalid reply attachment")
var attachmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Attachment embeds the original bytes so mailbox and report publication share
// one atomic record. Names are logical filenames, never host filesystem paths.
type Attachment struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"data"`
}

func ValidateAttachmentName(name string) error {
	if !attachmentName.MatchString(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf(
			"%w: use a filename of 1-128 ASCII letters, digits, dots, underscores or hyphens, starting with a letter or digit",
			ErrInvalidAttachment,
		)
	}
	return nil
}

func NewAttachment(name string, data []byte) (Attachment, error) {
	digest := sha256.Sum256(data)
	attachment := Attachment{Name: name, Size: int64(len(data)),
		SHA256: hex.EncodeToString(digest[:]), Data: append([]byte{}, data...)}
	if err := ValidateAttachments([]Attachment{attachment}); err != nil {
		return Attachment{}, err
	}
	return attachment, nil
}

// ValidateAttachments checks the complete set before any publication or export.
func ValidateAttachments(attachments []Attachment) error {
	if len(attachments) > MaxAttachments {
		return fmt.Errorf("%w: at most %d files", ErrInvalidAttachment, MaxAttachments)
	}
	names := make(map[string]struct{}, len(attachments))
	total := 0
	for _, attachment := range attachments {
		if err := ValidateAttachmentName(attachment.Name); err != nil {
			return err
		}
		name := strings.ToLower(attachment.Name)
		if _, exists := names[name]; exists {
			return fmt.Errorf("%w: duplicate name %q", ErrInvalidAttachment, attachment.Name)
		}
		names[name] = struct{}{}
		if attachment.Data == nil || len(attachment.Data) > MaxAttachmentBytes ||
			attachment.Size != int64(len(attachment.Data)) {
			return fmt.Errorf(
				"%w: missing bytes or invalid size for %q (maximum %d bytes)",
				ErrInvalidAttachment,
				attachment.Name,
				MaxAttachmentBytes,
			)
		}
		total += len(attachment.Data)
		if total > MaxTotalAttachmentBytes {
			return fmt.Errorf(
				"%w: total exceeds %d bytes",
				ErrInvalidAttachment,
				MaxTotalAttachmentBytes,
			)
		}
		digest := sha256.Sum256(attachment.Data)
		if attachment.SHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("%w: checksum mismatch for %q", ErrInvalidAttachment, attachment.Name)
		}
	}
	return nil
}

// CanonicalAttachments makes CLI argument order irrelevant to identical retries.
func CanonicalAttachments(attachments []Attachment) []Attachment {
	result := slices.Clone(attachments)
	slices.SortFunc(
		result,
		func(left, right Attachment) int { return strings.Compare(left.Name, right.Name) },
	)
	return result
}
