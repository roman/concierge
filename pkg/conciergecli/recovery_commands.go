package conciergecli

type closeRetiredCommand struct {
	TaskID     string `arg:"" help:"Held task with a retired worker or abandoned launch."`
	Generation string `required:"" help:"Exact retired generation to close."`
	Reason     string `required:"" help:"Reason for closing the retained work."`
	ClosedOn   string `required:"" help:"Closing date YYYY-MM-DD."`
}

func (closeRetiredCommand) Help() string {
	return `Preserves the hold, receipts, worktree and lease reservation.
An active worker or launch blocks closure. Inspect the task with backlog show.

Examples:
  concierge backlog close TASK_ID --generation GENERATION --reason "Superseded" --closed-on YYYY-MM-DD
  concierge --root /path/to/.concierge backlog close TASK_ID --generation GENERATION --reason "Cancelled" --closed-on YYYY-MM-DD`
}

type orphanRecoveryCommand struct {
	TaskID           string `arg:"" help:"Task with a pending local pooled worker restart."`
	Generation       string `required:"" help:"Current recorded worker generation; not the pending replacement."`
	Reason           string `required:"" help:"Reason for closing this orphaned work."`
	ClosedOn         string `required:"" help:"Closing date YYYY-MM-DD."`
	OperatorApproved bool   `help:"Confirm explicit operator approval for this task, generation and closure."`
}

func (orphanRecoveryCommand) Help() string {
	return `Requires explicit operator approval for this exact task, generation and closure.
Verifies absent Herdr resources and an idle held lease. Preserves the worktree,
commits, reports, pending messages and lease. Refuses remote and raw workers.

Examples:
  concierge backlog recover TASK_ID --generation GENERATION --reason "Superseded" --closed-on YYYY-MM-DD --operator-approved
  concierge --root /path/to/.concierge backlog recover TASK_ID --generation GENERATION --reason "Cancelled" --closed-on YYYY-MM-DD --operator-approved`
}
