package conciergecli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/taskops"
)

func (command *taskCommands) report(ctx context.Context, root string, output io.Writer) int {
	if command.Report.Attachment != "" || command.Report.Output != "" {
		if command.Report.Attachment == "" || command.Report.Output == "" ||
			command.Report.ReplyID == "" ||
			command.Report.Format != "toon" {
			return fail(
				output,
				2,
				fmt.Errorf(
					"attachment export requires a reply ID, --attachment and --output with the default format",
				),
				"use concierge task report TASK_ID REPLY_ID --attachment NAME --output NEW_FILE",
			)
		}
	}
	if command.Report.Format != "toon" && command.Report.ReplyID == "" {
		return fail(output, 2, fmt.Errorf("%s format requires a reply ID", command.Report.Format),
			"use concierge task report TASK_ID REPLY_ID --format body > reply.md")
	}
	service, err := taskops.New(root)
	if err != nil {
		return fail(output, 2, err, "use an initialized Concierge state root")
	}
	if command.Report.ReplyID == "" {
		reports, err := service.ListReports(ctx, command.Report.TaskID)
		if err != nil {
			return fail(
				output,
				1,
				err,
				"use concierge task report TASK_ID with an existing task ID",
			)
		}
		if _, err := fmt.Fprintf(output, "count: %d\nreports[%d]{reply_id,created_at}:\n", len(reports), len(reports)); err != nil {
			return 1
		}
		for _, report := range reports {
			if _, err := fmt.Fprintf(output, "  %s,%s\n", quote(report.ID.String()), quote(report.CreatedAt.Format(time.RFC3339Nano))); err != nil {
				return 1
			}
		}
		return 0
	}
	report, err := service.ReadReport(ctx, command.Report.TaskID, command.Report.ReplyID)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"use concierge task report TASK_ID to list saved reply IDs",
		)
	}
	if command.Report.Format == "body" {
		if _, err := io.WriteString(output, report.Body); err != nil {
			return 1
		}
		return 0
	}
	if command.Report.Attachment != "" {
		if err := exportReplyAttachment(report.Attachments, command.Report.Attachment, command.Report.Output); err != nil {
			return fail(
				output,
				1,
				err,
				"select an existing attachment name and a new --output file",
			)
		}
		if _, err := fmt.Fprintf(output, "attachment: %s\noutput: %s\n", quote(command.Report.Attachment), quote(command.Report.Output)); err != nil {
			return 1
		}
		return 0
	}
	if command.Report.Format == "json" {
		if err := json.NewEncoder(output).Encode(report); err != nil {
			return 1
		}
		return 0
	}
	if command.Report.Format == "concise" {
		if _, err := fmt.Fprintf(output, "status: archived\ntask: %s\ngeneration: %s\n", quote(report.Task), quote(report.SpawnGeneration)); err != nil {
			return 1
		}
		fullCommand := "concierge --root " + reportShellQuote(
			root,
		) + " task report " + reportShellQuote(
			command.Report.TaskID,
		) + " " + reportShellQuote(
			command.Report.ReplyID,
		) + " --format body"
		if err := writeReportPreview(output, command.Report.ReplyID, report.Body, fullCommand); err != nil {
			return 1
		}
		if err := writeAttachmentSummary(output, report.Attachments); err != nil {
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(output, "status: archived\ntask: %s\ngeneration: %s\nbody: %s\n", quote(report.Task), quote(report.SpawnGeneration), quote(report.Body)); err != nil {
		return 1
	}
	if err := writeAttachmentSummary(output, report.Attachments); err != nil {
		return 1
	}
	return 0
}
