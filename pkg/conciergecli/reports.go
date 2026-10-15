package conciergecli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/taskops"
)

func (command *taskCommands) report(ctx context.Context, root string, output io.Writer) int {
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
	if _, err := fmt.Fprintf(output, "status: archived\ntask: %s\ngeneration: %s\nbody: %s\n", quote(report.Task), quote(report.SpawnGeneration), quote(report.Body)); err != nil {
		return 1
	}
	return 0
}
