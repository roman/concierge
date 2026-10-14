package conciergecli

import (
	"context"
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/taskops"
)

func (command *taskCommands) report(ctx context.Context, root string, output io.Writer) int {
	service, err := taskops.New(root)
	if err != nil {
		return fail(output, 2, err, "use an initialized Concierge state root")
	}
	report, err := service.ReadScoutReport(ctx, command.Report.TaskID, command.Report.ReplyID)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"use concierge task report SCOUT_TASK_ID REPLY_ID with the reply's recorded ID",
		)
	}
	if _, err := fmt.Fprintf(output, "status: archived\ntask: %s\ngeneration: %s\nbody: %s\n", quote(report.Task), quote(report.SpawnGeneration), quote(report.Body)); err != nil {
		return 1
	}
	return 0
}
