package heartbeat

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/codexqueue"
	"github.com/roman/concierge/pkg/primary"
)

type Attempt struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	Phase      string    `json:"phase"`
	Count      int       `json:"count"`
	At         time.Time `json:"at"`
}

func deliveryPlan(event *Event, now time.Time) string {
	if event.Claim != nil && now.Before(event.Claim.Until) {
		return "processing"
	}
	attempt := event.Attempt
	if attempt == nil {
		return "submit"
	}
	if attempt.Count < 0 || attempt.Count > 3 {
		return "unavailable"
	}
	switch attempt.Phase {
	case "submitting", "uncertain", "accepted", "offered", "failed", "deferred":
	default:
		return "unavailable"
	}
	if attempt.Phase == "submitting" || attempt.Phase == "uncertain" {
		return "reconcile"
	}
	if (attempt.Phase == "accepted" || attempt.Phase == "offered") &&
		now.Before(attempt.At.Add(ClaimLease)) {
		return attempt.Phase
	}
	if attempt.Phase == "failed" &&
		now.Before(attempt.At.Add(time.Duration(1<<attempt.Count)*5*time.Second)) {
		return "backoff"
	}
	if attempt.Count >= 3 {
		return "retry_exhausted"
	}
	return "submit"
}

func attemptClientID(event Event) string {
	return event.ID + "." + event.Attempt.Generation + "." + event.Attempt.ID
}

func shellQuote(
	value string,
) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func Notification(
	executable, root, messageRoot, recipient string,
	event Event,
	generation string,
) string {
	return fmt.Sprintf(
		"Concierge supervisor event %s. Recheck this event against fresh state before acting. Run %s --root %s --message-root %s --recipient %s heartbeat claim %s --generation %s. Act only on status: current. If the native sandbox hides process identity or blocks the owning Unix socket, retry the exact command through the normal native approval flow (Codex shell sandbox_permissions=require_escalated). Preserve the configured permissions, trust, owner, and binding. Use that same approved access for the later acknowledgement. Read fresh pending messages through the normal bound task-reply protocol. Post the operator update before recording its output acknowledgement. If the claim is stale, paused, in progress, or acknowledged, do not repeat the update.",
		event.ID,
		shellQuote(executable),
		shellQuote(root),
		shellQuote(messageRoot),
		shellQuote(recipient),
		shellQuote(event.ID),
		shellQuote(generation),
	)
}

// DeliverCodex journals an attempt before queue insertion. A crash around the
// insertion leaves a reconcilable uncertainty rather than permission to resend.
func (store Store) DeliverCodex(
	ctx context.Context,
	executable, messageRoot, recipient string,
	now time.Time,
) error {
	var client *codexqueue.Client
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	var prepared Event
	err := primary.WithOwner(store.Root, func(owner primary.Identity) error {
		err := store.update(ctx, func(state *State) error {
			event := state.Pending
			if event == nil || !state.Enabled {
				return nil
			}
			if err := available(state); err != nil {
				state.Delivery = "paused"
				return nil
			}
			if event.MessageID == "" {
				state.Delivery = "pending_publication"
				return nil
			}
			if state.Binding == nil {
				state.Delivery = "unavailable"
				state.LastError = "native destination missing; register the owning primary explicitly"
				return nil
			}
			binding := state.Binding
			if err := bindingCurrent(state, owner, binding.Generation, now); err != nil {
				return err
			}
			if binding.Destination.Mode != Push {
				return nil
			}
			plan := deliveryPlan(event, now)
			if plan != "submit" && plan != "reconcile" {
				state.Delivery = plan
				return nil
			}
			var err error
			client, err = codexqueue.Open(
				ctx,
				binding.Destination.Socket,
				binding.Destination.Server,
			)
			if err != nil {
				state.Delivery = "unavailable"
				state.LastError = err.Error()
				return nil
			}
			if plan == "reconcile" {
				found, err := client.Contains(
					ctx,
					binding.Destination.Thread,
					attemptClientID(*event),
				)
				if err != nil || !found {
					state.Delivery = "uncertain"
					if err != nil {
						state.LastError = err.Error()
					} else {
						state.LastError = "submission remains uncertain; absence from the queue and bounded history is not a negative receipt"
					}
					if event.Claim == nil {
						return nil
					}
				} else {
					event.Attempt.Phase = "accepted"
					event.Attempt.At = now.UTC()
					state.Delivery, state.LastError = "accepted", ""
					return nil
				}
			}
			if event.Attempt != nil && event.Attempt.Count >= 3 {
				state.Delivery = "retry_exhausted"
				return nil
			}
			gate, err := client.Readiness(ctx, binding.Destination.Thread, filepath.Dir(store.Root))
			if err != nil {
				state.Delivery = "unavailable"
				state.LastError = err.Error()
				return nil
			}
			if gate != codexqueue.Ready {
				state.Delivery = "deferred_" + string(gate)
				return nil
			}
			count := 1
			if event.Attempt != nil {
				count = event.Attempt.Count + 1
			}
			event.Claim = nil
			event.Attempt = &Attempt{
				ID:         rand.Text(),
				Generation: binding.Generation,
				Phase:      "submitting",
				Count:      count,
				At:         now.UTC(),
			}
			state.Delivery, state.LastError = "submitting", ""
			prepared = *event
			return nil
		})
		if err != nil || prepared.ID == "" {
			return err
		}
		return store.update(ctx, func(state *State) error {
			event := state.Pending
			if event == nil || event.ID != prepared.ID || event.Attempt == nil ||
				event.Attempt.ID != prepared.Attempt.ID {
				return nil
			}
			if err := bindingCurrent(state, owner, prepared.Attempt.Generation, time.Now()); err != nil {
				return err
			}
			if err := available(state); err != nil {
				event.Attempt.Phase = "deferred"
				event.Attempt.Count--
				state.Delivery = "paused"
				return nil
			}
			gate, err := client.Readiness(
				ctx,
				state.Binding.Destination.Thread,
				filepath.Dir(store.Root),
			)
			if err != nil || gate != codexqueue.Ready {
				event.Attempt.Phase = "deferred"
				event.Attempt.Count--
				state.Delivery = "deferred_" + string(gate)
				if err != nil {
					state.LastError = err.Error()
				}
				return nil
			}
			notification := Notification(
				executable,
				store.Root,
				messageRoot,
				recipient,
				*event,
				state.Binding.Generation,
			)
			err = client.Submit(
				ctx,
				state.Binding.Destination.Thread,
				attemptClientID(*event),
				notification,
			)
			event.Attempt.At = time.Now().UTC()
			switch {
			case err == nil:
				event.Attempt.Phase = "accepted"
				state.Delivery, state.LastError = "accepted", ""
			case errors.Is(err, codexqueue.ErrUncertain):
				event.Attempt.Phase = "uncertain"
				state.Delivery, state.LastError = "uncertain", err.Error()
			default:
				event.Attempt.Phase = "failed"
				state.Delivery, state.LastError = "failed", err.Error()
			}
			return nil
		})
	})
	return err
}
