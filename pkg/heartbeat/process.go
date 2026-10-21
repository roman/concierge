package heartbeat

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/codexqueue"
)

var ErrAcknowledged = errors.New("event output is already acknowledged")

const inboxWakeGrace = time.Minute

func claimEvent(state *State, id string, now time.Time) (Claim, error) {
	event, err := pending(state, id)
	if err != nil {
		return Claim{}, err
	}
	if event.Claim != nil && now.Before(event.Claim.Until) {
		return Claim{}, errors.New("event processing is in progress")
	}
	claim := Claim{Token: rand.Text(), Until: now.Add(ClaimLease).UTC()}
	event.Claim = &claim
	return claim, nil
}

func acknowledgeEvent(state *State, id, token, output string, now time.Time) error {
	if strings.TrimSpace(output) == "" {
		return errors.New("post the operator update first; output evidence is required")
	}
	if state.Receipt != nil && state.Receipt.EventID == id && state.Receipt.Token == token &&
		state.Receipt.Output == output {
		return nil
	}
	event, err := pending(state, id)
	if err != nil {
		return err
	}
	if event.Claim == nil || token == "" || event.Claim.Token != token ||
		!now.Before(event.Claim.Until) {
		return errors.New("claim is missing or expired")
	}
	if event.Kind == Periodic {
		if state.LastSnapshot == event.Snapshot {
			state.QuietCount++
		} else {
			state.QuietCount = 0
		}
		state.LastSnapshot = event.Snapshot
	}
	state.SeenReplies = append([]string(nil), event.Replies...)
	state.Receipt = &Receipt{
		SnapshotHash: snapshotHash(event.Snapshot),
		EventID:      id,
		Token:        token,
		Output:       output,
		At:           now.UTC(),
		MessageID:    event.MessageID,
	}
	if event.Interval > 0 {
		interval := event.Interval
		if event.Kind == Inbox {
			interval = max(interval, inboxWakeGrace)
		}
		state.Next = now.Add(interval).UTC()
	}
	state.Delivery, state.LastError = "output_reported", ""
	state.Pending = nil
	return nil
}

func (store Store) verifyDestination(ctx context.Context, destination Destination) error {
	if destination.Mode != Push {
		return ValidateClaudeHooks(filepath.Dir(store.Root))
	}
	client, err := codexqueue.Open(ctx, destination.Socket, destination.Server)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Verify(ctx, destination.Thread, filepath.Dir(store.Root))
}

// ClaimCurrent fetches state after the generation and destination checks. Only
// that observed snapshot and message set can be covered by the output receipt.
func (store Store) ClaimCurrent(
	ctx context.Context,
	id, generation string,
	observe func() (Observation, error),
	now time.Time,
) (Event, error) {
	var result Event
	err := store.withBinding(ctx, generation, now, func(state *State) error {
		if state.Receipt != nil && state.Receipt.EventID == id {
			return ErrAcknowledged
		}
		event, err := pending(state, id)
		if err != nil {
			return err
		}
		if event.MessageID == "" {
			return errors.New("event publication is not confirmed")
		}
		if event.Claim != nil && now.Before(event.Claim.Until) {
			return errors.New("event processing is in progress")
		}
		fresh, err := observe()
		if err != nil {
			return err
		}
		if _, err := claimEvent(state, id, now); err != nil {
			return err
		}
		event.Snapshot, event.Replies = fresh.Text, append([]string(nil), fresh.Replies...)
		event.Notify = fresh.Notify
		state.Delivery = "processing"
		result = *event
		return nil
	})
	return result, err
}

func (store Store) AcknowledgeCurrent(
	ctx context.Context,
	id, generation, token, output string,
	now time.Time,
) error {
	return store.withBinding(
		ctx,
		generation,
		now,
		func(state *State) error { return acknowledgeEvent(state, id, token, output, now) },
	)
}
