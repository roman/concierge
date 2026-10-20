package heartbeat

import (
	"context"
	"path/filepath"
	"time"

	"github.com/roman/tincan/pkg/inbox"
)

// Tick is called only by the root's leased host. Native delivery follows
// publication; it never owns the schedule.
func Tick(
	ctx context.Context,
	root, messageRoot string,
	recipient inbox.Recipient,
	herdr string,
	now time.Time,
) (State, error) {
	store := Store{Root: root}
	state, err := store.Read(ctx)
	if err != nil || !state.Enabled {
		return state, err
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return state, err
	}
	pending, err := messages.List(recipient)
	if err != nil {
		return state, err
	}
	newReplies := unseen(pendingIDs(pending, root), state.SeenReplies)
	if state.Pending != nil && newReplies && state.Pending.Kind == Periodic {
		if err := store.update(ctx, func(current *State) error {
			if current.Pending != nil && current.Pending.ID == state.Pending.ID {
				promoteInbox(current.Pending, now)
			}
			return nil
		}); err != nil {
			return state, err
		}
	}
	if state.Pending == nil && (newReplies || (!state.Paused && !now.Before(state.Next))) {
		settings, err := ReadSettings(filepath.Join(root, "config", "heartbeat.json"))
		if err != nil {
			return state, err
		}
		observation, err := Collect(ctx, root, messageRoot, recipient.String(), herdr)
		if err != nil {
			return state, err
		}
		if _, err := store.Schedule(ctx, now, observation, settings); err != nil {
			return state, err
		}
	}
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		return state, err
	}
	return store.Read(ctx)
}
