package taskview

import (
	"fmt"
	"io"

	"github.com/roman/concierge/pkg/taskstate"
)

func writeRemoteResult(output io.Writer, result taskstate.RemoteResult) error {
	if result.Commit == "" {
		return nil
	}
	if _, err := fmt.Fprintf(output, "remote_result:\n  phase: %s\n  commit: %s\n  primary_holder: %s\n", result.Phase, quote(result.Commit), quote(result.Holder)); err != nil {
		return err
	}
	if result.Lease.ID == "" {
		_, err := fmt.Fprintln(output, "  primary_lease: null")
		return err
	}
	lease := result.Lease
	_, err := fmt.Fprintf(
		output,
		"  primary_lease:\n    id: %s\n    state: %s\n    project: %s\n    worktree: %s\n    branch: %s\n",
		quote(lease.ID),
		lease.State,
		quote(lease.Project),
		quote(lease.Path),
		quote(lease.Branch),
	)
	return err
}
