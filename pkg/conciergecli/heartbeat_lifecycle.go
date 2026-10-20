package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/codexqueue"
	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/primary"
)

func hostOwnsHeartbeat(ctx context.Context, root string) (bool, error) {
	state, err := (heartbeat.Store{Root: root}).Read(ctx)
	return state.Enabled || state.PauseReason != "activation required", err
}

func writeHeartbeatState(ctx context.Context, root string, destination io.Writer) int {
	state, err := (heartbeat.Store{Root: root}).Read(ctx)
	if err != nil {
		return fail(destination, 1, err, heartbeatHelp)
	}
	var response strings.Builder
	output := &response
	enabled, paused := "no", "no"
	if state.Enabled {
		enabled = "yes"
	}
	if state.Paused {
		paused = "yes"
	}
	fmt.Fprintf(
		output,
		"enabled: %s\noperator_paused: %s\npause_reason: %s\ndelivery: %s\nquiet_count: %d\n",
		enabled,
		paused,
		quote(state.PauseReason),
		quote(state.Delivery),
		state.QuietCount,
	)
	if state.Enabled {
		generation := ""
		if state.Binding != nil {
			generation = state.Binding.Generation
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		bindingErr := (heartbeat.Store{Root: root}).CheckBinding(check, generation, time.Now())
		cancel()
		if bindingErr != nil {
			fmt.Fprintf(
				output,
				"native_status: unavailable\nnative_error: %s\n",
				quote(bindingErr.Error()),
			)
		} else {
			fmt.Fprintln(output, "native_status: available")
		}
	}
	if state.Binding != nil {
		fmt.Fprintf(
			output,
			"native_mode: %s\ngeneration: %s\n",
			state.Binding.Destination.Mode,
			state.Binding.Generation,
		)
		if !state.Binding.Destination.Expires.IsZero() {
			fmt.Fprintf(
				output,
				"consumer_expires: %s\n",
				state.Binding.Destination.Expires.Format(time.RFC3339),
			)
		}
	}
	if state.Pending != nil {
		fmt.Fprintf(
			output,
			"pending_event: %s\nevent_kind: %s\n",
			state.Pending.ID,
			state.Pending.Kind,
		)
		if state.Pending.Attempt != nil {
			fmt.Fprintf(output, "attempts: %d\n", state.Pending.Attempt.Count)
		}
	}
	if state.Receipt != nil {
		fmt.Fprintf(output, "last_output: %s\n", quote(state.Receipt.Output))
	}
	if state.LastError != "" {
		fmt.Fprintf(output, "last_error: %s\n", quote(state.LastError))
	}
	if !state.Enabled {
		fmt.Fprintln(
			output,
			"help: Enable intentionally with concierge heartbeat enable; register the owning native destination before expecting delivery.",
		)
	}
	if _, err := io.WriteString(destination, response.String()); err != nil {
		return 1
	}
	return 0
}

func (command *cli) runHeartbeatLifecycle(
	ctx context.Context,
	root, selected string,
	destination io.Writer,
) int {
	var response strings.Builder
	code := command.heartbeatLifecycleResponse(ctx, root, selected, &response)
	if _, err := io.WriteString(destination, response.String()); err != nil {
		return 1
	}
	return code
}

func (command *cli) heartbeatLifecycleResponse(
	ctx context.Context,
	root, selected string,
	output io.Writer,
) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store := heartbeat.Store{Root: root}
	var err error
	switch selected {
	case "heartbeat enable":
		err = store.Enable(ctx, time.Now())
	case "heartbeat pause":
		err = store.Pause(ctx, command.Heartbeat.Pause.Reason)
	case "heartbeat resume":
		err = store.Resume(ctx)
	case "heartbeat register codex":
		selection := command.Heartbeat.Register.Codex
		var client *codexqueue.Client
		client, err = codexqueue.Open(ctx, selection.Socket, primary.ProcessToken{})
		if err == nil {
			defer client.Close()
			_, err = store.Register(
				ctx,
				heartbeat.Destination{
					Mode:   heartbeat.Push,
					Socket: selection.Socket,
					Thread: selection.Thread,
					Server: client.Server,
				},
				func(primary.Identity) error { return client.Verify(ctx, selection.Thread, filepath.Dir(root)) },
				time.Now(),
			)
		}
	case "heartbeat claim <event-id>":
		selection := command.Heartbeat.Claim
		messageRoot, rootErr := rootWithDefault(command.MessageRoot, ".tincan")
		if rootErr != nil {
			return fail(output, 2, rootErr, heartbeatHelp)
		}
		var event heartbeat.Event
		event, err = store.ClaimCurrent(
			ctx,
			selection.EventID,
			selection.Generation,
			func() (heartbeat.Observation, error) {
				return heartbeat.Collect(ctx, root, messageRoot, command.Recipient, selection.Herdr)
			},
			time.Now(),
		)
		if errors.Is(err, heartbeat.ErrAcknowledged) {
			fmt.Fprintln(output, "status: acknowledged\nhelp: Do not repeat the operator output.")
			return 0
		}
		if err == nil {
			fmt.Fprintf(
				output,
				"status: current\nevent: %s\ngeneration: %s\nkind: %s\nclaim_token: %s\nclaim_until: %s\n",
				event.ID,
				selection.Generation,
				event.Kind,
				event.Claim.Token,
				event.Claim.Until.Format(time.RFC3339),
			)
			io.WriteString(output, event.Snapshot)
			if event.Notify {
				fmt.Fprintln(
					output,
					"notification: Notify the operator through the harness notification tool when a decision or finished worker waits.",
				)
			}
			if event.Kind == heartbeat.Inbox {
				fmt.Fprintln(
					output,
					"help: Process the new pending messages through their normal verification protocol. Report only their resulting work. Do not use an inbox event to send a periodic reminder.",
				)
			}
			fmt.Fprintln(
				output,
				"help: Post the operator update, then run concierge heartbeat ack EVENT --generation GENERATION --token CLAIM_TOKEN --output OUTPUT_REFERENCE. This records a primary-reported output receipt.",
			)
			return 0
		}
	case "heartbeat ack <event-id>":
		selection := command.Heartbeat.Ack
		err = store.AcknowledgeCurrent(
			ctx,
			selection.EventID,
			selection.Generation,
			selection.Token,
			selection.Output,
			time.Now(),
		)
		if err == nil {
			fmt.Fprintln(output, "status: output_reported")
			return 0
		}
	default:
		return fail(output, 2, errors.New("unknown heartbeat lifecycle command"), heartbeatHelp)
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"inspect concierge heartbeat show; preserve the pending event and post output before acknowledgement",
		)
	}
	return writeHeartbeatState(ctx, root, output)
}
