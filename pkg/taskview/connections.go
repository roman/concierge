package taskview

import (
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
)

func writeConnections(output io.Writer, worker *taskstate.Worker) error {
	if _, err := fmt.Fprintf(output, "machine: %s\n", quote(worker.Machine)); err != nil {
		return err
	}
	connections := taskstate.Connections{}
	if worker.Connections != nil {
		connections = *worker.Connections
	}
	if err := writeConnection(output, "herdr", connections.Herdr); err != nil {
		return err
	}
	return writeConnection(output, "tunnel", connections.Tunnel)
}

func writeConnection(output io.Writer, channel string, state taskstate.ConnectionState) error {
	status := string(state.Status)
	if status == "" {
		status = "unknown"
	}
	lastError := "null"
	if state.LastError != "" {
		lastError = quote(state.LastError)
	}
	_, err := fmt.Fprintf(
		output,
		"%s_reachability:\n  status: %s\n  last_success: %s\n  last_error: %s\n  next_retry: %s\n",
		channel,
		status,
		connectionTime(state.LastSuccess),
		lastError,
		connectionTime(state.NextRetry),
	)
	return err
}

func connectionTime(value time.Time) string {
	if value.IsZero() {
		return "null"
	}
	return quote(value.UTC().Format(time.RFC3339Nano))
}
