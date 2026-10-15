package taskview

import (
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

// WriteStuckLaunches reports unbound launches that need operator inspection.
func WriteStuckLaunches(output io.Writer, tasks []taskstate.Task, now time.Time) error {
	var stuck []taskstate.Task
	for _, task := range tasks {
		if task.UnregisteredLaunchOverdue(now) {
			stuck = append(stuck, task)
		}
	}
	if len(stuck) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(output, "stuck_launches[%d]{task,phase,session,pane_id,generation,deadline,recovery}:\n", len(stuck)); err != nil {
		return err
	}
	for _, task := range stuck {
		intent := task.Snapshot().LaunchIntent
		deadline := "unknown (legacy launch)"
		if !intent.AgentRequestedAt.IsZero() {
			deadline = intent.AgentRequestedAt.Add(herdr.WorktreeStartupTimeout).
				Format(time.RFC3339Nano)
		}
		if _, err := fmt.Fprintf(output,
			"  %s,%s,%s,%s,%s,%s,%s\n",
			quote(task.ID()), intent.Phase, quote(intent.Session), quote(intent.Endpoint.Pane),
			quote(intent.Generation), quote(deadline),
			quote("Inspect the Herdr pane. Close verified launch resources before running concierge task abandon "+task.ID()+" --generation "+intent.Generation),
		); err != nil {
			return err
		}
	}
	return nil
}
