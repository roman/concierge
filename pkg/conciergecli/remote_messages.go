package conciergecli

import (
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/taskops"
)

type remoteTaskSelection struct {
	TaskID     string `arg:"" help:"Assigned task ID."`
	Generation string `required:"" help:"Generation inherited at worker launch."`
}

type remoteInboxCommands struct {
	List struct {
		TaskID string `arg:"" help:"Assigned task ID."`
	} `cmd:"" help:"List pending IDs and keys without reading assignment bodies."`
}

type remoteBootstrapSelection struct {
	TaskID     string `arg:"" help:"Assigned task ID."`
	MessageID  string `arg:"" help:"Bootstrap message ID from the fresh pending list."`
	Generation string `required:"" help:"Generation inherited at worker launch."`
}

type remoteBootstrapCommands struct {
	Read remoteBootstrapSelection `cmd:"" help:"Read a bootstrap only after the primary verifies its exact launch context."`
	Ack  remoteBootstrapSelection `cmd:"" help:"Acknowledge a matching bootstrap without receiving new work."`
}

func writeRemotePending(output io.Writer, pending []taskops.PendingMessage) int {
	if _, err := fmt.Fprintf(output, "count: %d\nmessages[%d]{id,key,created_at}:\n", len(pending), len(pending)); err != nil {
		return 1
	}
	for _, message := range pending {
		if _, err := fmt.Fprintf(output, "  %s,%s,%s\n", quote(message.ID), quote(message.Key), quote(message.CreatedAt.UTC().Format(time.RFC3339Nano))); err != nil {
			return 1
		}
	}
	return 0
}

func writeRemoteBootstrap(output io.Writer, selected string, bootstrap taskops.BootstrapInfo) int {
	if _, err := fmt.Fprintf(output, "kind: task-bootstrap\ntask: %s\ngeneration: %s\n", quote(bootstrap.Task), quote(bootstrap.Generation)); err != nil {
		return 1
	}
	if selected == "task bootstrap ack <task-id> <message-id>" {
		if _, err := fmt.Fprintln(output, "status: acknowledged"); err != nil {
			return 1
		}
	}
	return 0
}
