package conciergecli

import (
	"context"
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/taskops"
)

const replySendUsage = "concierge [--recipient NAME] task reply send TASK_ID --generation GEN --key KEY [--assignment-id ID] --body-file FILE|- [--attach NAME=PATH ...]"
const replyCheckUsage = "concierge [--recipient NAME] task reply check MESSAGE_ID"

type replyCommands struct {
	Reconcile struct {
		File string `required:"" help:"Retained outbox record; use - for stdin. Its original tuple is never changed."`
	} `cmd:"" help:"Ask the primary host to reconcile an identical original reply after access is revoked."`
	Send struct {
		TaskID       string   `arg:"" help:"Task ID."`
		Generation   string   `required:"" help:"Worker generation supplied at launch."`
		Key          string   `required:"" help:"Stable logical reply key for identical retries."`
		AssignmentID string   `help:"Assignment to acknowledge after reply publication."`
		BodyFile     string   `required:"" help:"Result body file; use - for stdin."`
		Attach       []string `sep:"none" help:"Named attachment NAME=PATH; repeat for up to 8 regular files, 256 KiB each and 512 KiB total."`
	} `cmd:"" help:"Publish a worker result and optionally acknowledge its assignment."`
	Check struct {
		MessageID string `arg:"" help:"Reply message ID in the primary inbox."`
		Format    string `default:"toon" enum:"toon,concise" help:"Output format. Concise previews the outcome, headings and decisions; toon includes the full current body."`
	} `cmd:"" help:"Read current reply content without accepting or acknowledging it."`
}

func (command *replyCommands) send(
	ctx context.Context,
	service *taskops.Service,
	messageRoot string,
	stdin io.Reader,
	output io.Writer,
) int {
	attachments, err := readReplyAttachments(command.Send.Attach)
	if err != nil {
		return fail(output, 1, err, replySendUsage)
	}
	body, err := readBodyFile(command.Send.BodyFile, stdin)
	if err != nil {
		return fail(output, 1, err, "provide a readable --body-file or use --body-file - for stdin")
	}
	input := taskops.ReplyInput{
		TaskID:       command.Send.TaskID,
		Generation:   command.Send.Generation,
		Key:          command.Send.Key,
		AssignmentID: command.Send.AssignmentID,
		Body:         body,
		Attachments:  attachments,
	}
	result, err := service.SendReply(ctx, messageRoot, input)
	if result.Message.ID.String() != "" {
		if _, writeErr := fmt.Fprintf(output, "message_id: %s\nassignment: %s\n", quote(result.Message.ID.String()), result.Assignment); writeErr != nil {
			return 1
		}
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			replySendUsage+"; inspect the reply and assignment, then retry with identical arguments and body",
		)
	}
	return 0
}

func (command *replyCommands) check(
	ctx context.Context,
	service *taskops.Service,
	messageRoot string,
	fullCommand string,
	output io.Writer,
) int {
	result, err := service.CheckReply(ctx, messageRoot, command.Check.MessageID)
	if err != nil {
		return fail(
			output,
			1,
			err,
			replyCheckUsage+"; inspect the selected primary mailbox and bound assignment",
		)
	}
	if _, err := fmt.Fprintf(output, "status: %s\ntask: %s\n", result.State, quote(result.TaskID)); err != nil {
		return 1
	}
	if result.State != taskops.ReplyCurrent {
		return 0
	}
	if _, err := fmt.Fprintf(output, "generation: %s\n", quote(result.Generation)); err != nil {
		return 1
	}
	if result.AssignmentID != "" {
		if _, err := fmt.Fprintf(output, "assignment_id: %s\n", quote(result.AssignmentID)); err != nil {
			return 1
		}
	}
	if command.Check.Format == "concise" {
		if err := writeReportPreview(output, command.Check.MessageID, result.Body, fullCommand); err != nil {
			return 1
		}
		if err := writeAttachmentSummary(output, result.Attachments); err != nil {
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(output, "body: %s\n", quote(result.Body)); err != nil {
		return 1
	}
	if err := writeAttachmentSummary(output, result.Attachments); err != nil {
		return 1
	}
	return 0
}
