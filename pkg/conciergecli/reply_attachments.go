package conciergecli

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/roman/concierge/pkg/taskprotocol"
)

func readReplyAttachments(specifications []string) ([]taskprotocol.Attachment, error) {
	if len(specifications) > taskprotocol.MaxAttachments {
		return nil, fmt.Errorf("at most %d attachments", taskprotocol.MaxAttachments)
	}
	var attachments []taskprotocol.Attachment
	for _, specification := range specifications {
		name, path, found := strings.Cut(specification, "=")
		if !found || path == "" || path == "-" {
			return nil, errors.New("attachment requires NAME=PATH to a regular file")
		}
		if err := taskprotocol.ValidateAttachmentName(name); err != nil {
			return nil, err
		}
		attachment, err := readReplyAttachment(name, path)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := taskprotocol.ValidateAttachments(attachments); err != nil {
		return nil, err
	}
	return taskprotocol.CanonicalAttachments(attachments), nil
}

func readReplyAttachment(name, path string) (taskprotocol.Attachment, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return taskprotocol.Attachment{}, fmt.Errorf("attachment %q: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return taskprotocol.Attachment{}, errors.New(
			"attachment source must be a regular file, without a final symlink",
		)
	}
	if info.Size() > taskprotocol.MaxAttachmentBytes {
		return taskprotocol.Attachment{}, errors.New("attachment exceeds 256 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, taskprotocol.MaxAttachmentBytes+1))
	if err != nil {
		return taskprotocol.Attachment{}, err
	}
	return taskprotocol.NewAttachment(name, data)
}

func writeAttachmentSummary(output io.Writer, attachments []taskprotocol.Attachment) error {
	if len(attachments) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(output, "attachments[%d]{name,size,sha256}:\n", len(attachments)); err != nil {
		return err
	}
	for _, attachment := range attachments {
		if _, err := fmt.Fprintf(output, "  %s,%d,%s\n", quote(attachment.Name), attachment.Size, quote(attachment.SHA256)); err != nil {
			return err
		}
	}
	return nil
}

func exportReplyAttachment(attachments []taskprotocol.Attachment, name, path string) error {
	if strings.HasSuffix(path, string(os.PathSeparator)) {
		return errors.New("attachment output must be a new file path without a trailing separator")
	}
	if err := taskprotocol.ValidateAttachments(attachments); err != nil {
		return err
	}
	if err := taskprotocol.ValidateAttachmentName(name); err != nil {
		return err
	}
	for _, attachment := range attachments {
		if attachment.Name != name {
			continue
		}
		root, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer root.Close()
		temporary := ".concierge-attachment-" + rand.Text()
		file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer root.Remove(temporary)
		_, writeError := file.Write(attachment.Data)
		if writeError == nil {
			writeError = file.Sync()
		}
		err = errors.Join(writeError, file.Close())
		if err != nil {
			return err
		}
		// A hard link publishes complete bytes and refuses every existing target.
		return root.Link(temporary, filepath.Base(path))
	}
	return fmt.Errorf("attachment %q is absent from this reply", name)
}
