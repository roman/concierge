package taskstate

// CheckResultTransition rejects invalid requests before delivery resources are
// acquired. AcceptResult also requires any selected remote result to be ready.
func (task Task) CheckResultTransition(
	generation string,
	mode CompletionMode,
	closedOn string,
) error {
	if task.record.LaunchIntent != nil {
		return ErrInvalidTransition
	}
	switch mode {
	case Close:
		if closedOn == "" || !validDate(closedOn) ||
			(task.State() == Done && task.record.Closed != closedOn) {
			return ErrInvalidTransition
		}
	case Retain:
		if closedOn != "" {
			return ErrInvalidTransition
		}
	default:
		return ErrInvalidTransition
	}
	if task.record.Completion == nil && task.record.Worker == nil && task.record.Receipt != nil {
		receipt := task.record.Receipt
		if receipt.Generation == generation && receipt.Mode == mode &&
			((mode == Close && task.State() == Done) || (mode == Retain && task.State() == Queued)) {
			return nil
		}
		return ErrInvalidTransition
	}
	if completion := task.record.Completion; completion != nil {
		if completion.Generation != generation || completion.Mode != mode {
			return ErrInvalidTransition
		}
		if (mode == Close && task.State() == Done) || (mode == Retain && task.State() == Queued) {
			return nil
		}
		if task.State() != InFlight {
			return ErrInvalidTransition
		}
	}
	if task.record.Completion == nil {
		worker := task.record.Worker
		if task.State() != InFlight || worker == nil || worker.Generation != generation ||
			worker.PendingAdoption || worker.PendingGeneration != "" || worker.PendingLaunch != "" {
			return ErrInvalidTransition
		}
	}
	if (mode == Close && task.record.Hold != nil) || (mode == Retain && task.record.Hold == nil) {
		return ErrInvalidTransition
	}
	return nil
}
