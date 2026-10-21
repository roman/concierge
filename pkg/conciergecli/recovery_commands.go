package conciergecli

type closeRetiredCommand struct {
	TaskID     string `arg:"" help:"Held retired task or orphaned confirmed local worker."`
	Generation string `required:"" help:"Exact recorded generation to close."`
	Reason     string `required:"" help:"Reason for closing the retained work."`
	ClosedOn   string `required:"" help:"Closing date YYYY-MM-DD."`
}

func (closeRetiredCommand) Help() string {
	return `Preserves the hold, receipts, worktree and lease reservation.
A bound local worker requires verified pane absence and no pending current reply.
Pending launches and remote or raw workers block closure. Inspect backlog show.

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
