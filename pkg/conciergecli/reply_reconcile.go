package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/replyoutbox"
)

func (command *cli) reconcileReply(
	ctx context.Context,
	root string,
	stdin io.Reader,
	output io.Writer,
) int {
	record, err := readRetainedReply(command.Task.Reply.Reconcile.File, stdin)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"provide the complete original outbox record; retain it until all delivery stages are confirmed",
		)
	}
	if record.Payload.Root != root {
		return fail(
			output,
			1,
			errors.New("original reply belongs to another primary root"),
			"select the original primary root without changing the retained reply",
		)
	}
	response, err := hostservice.Call(ctx, filepath.Join(root, "host.sock"), hostservice.Request{
		Operation: hostservice.ReconcileReply, Expectations: &command.hostExpectations,
		Reconciliation: &record.Payload,
	})
	if reply := response.Reconciled; reply != nil {
		if _, writeError := fmt.Fprintf(output, "message_id: %s\ngeneration: %s\npublication: %s\narchive: %s\nassignment: %s\n",
			quote(reply.MessageID), quote(record.Payload.Generation), reply.Publication, reply.Archive, reply.Assignment); writeError != nil {
			return 1
		}
	}
	if err == nil && response.Outcome != hostservice.Completed {
		err = errors.New(response.Error)
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"retain the original outbox record and retry it unchanged through the primary host",
		)
	}
	return 0
}

func readRetainedReply(path string, stdin io.Reader) (replyoutbox.Record, error) {
	reader := stdin
	if path != "-" {
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return replyoutbox.Record{}, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return replyoutbox.Record{}, errors.New("retained reply must be a regular file")
		}
		reader = file
	}
	if reader == nil {
		return replyoutbox.Record{}, errors.New("retained reply input is missing")
	}
	data, err := io.ReadAll(io.LimitReader(reader, 3<<20))
	if err != nil {
		return replyoutbox.Record{}, err
	}
	return replyoutbox.DecodeRecord(data)
}
