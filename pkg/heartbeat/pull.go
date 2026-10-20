package heartbeat

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

type ActivityGate string

const (
	Ready      ActivityGate = "ready"
	Busy       ActivityGate = "busy"
	Permission ActivityGate = "permission"
	Unknown    ActivityGate = "unknown"
)

type Activity struct {
	Owner primary.Identity `json:"owner"`
	Gate  ActivityGate     `json:"gate"`
}

func pullConsumerAvailable(state *State, now time.Time) error {
	if event := state.Pending; event != nil {
		if event.Claim != nil && now.Before(event.Claim.Until) {
			return nil
		}
		if event.Attempt != nil && event.Attempt.Phase == "offered" &&
			now.Before(event.Attempt.At.Add(ClaimLease)) {
			return nil
		}
	}
	if state.Binding.Consumer == nil {
		return errors.New("primary delivery unavailable: Stop consumer missing")
	}
	status, err := primary.ProbeProcessToken(*state.Binding.Consumer)
	if err != nil {
		return err
	}
	if status != primary.ProcessRunning {
		return errors.New("primary delivery unavailable: Stop consumer exited")
	}
	return nil
}

// RecordActivity is called by synchronous lifecycle hooks. The asynchronous
// consumer cannot mark a session ready after a later prompt has started.
func (store Store) RecordActivity(
	ctx context.Context,
	identity primary.Identity,
	gate ActivityGate,
) error {
	switch gate {
	case Ready, Busy, Permission, Unknown:
	default:
		return errors.New("unknown Claude lifecycle state")
	}
	return primary.WithCallerOwner(
		store.Root,
		primary.Caller{PID: os.Getpid(), UID: uint32(os.Geteuid())},
		func(owner primary.Identity) error {
			if owner != identity || owner.Kind != "claude" {
				return errors.New("Claude activity does not belong to the primary")
			}
			return store.update(ctx, func(state *State) error {
				state.Activity = &Activity{Owner: owner, Gate: gate}
				return nil
			})
		},
	)
}

func offerPull(state *State, now time.Time) *Event {
	if !state.Enabled || state.Pending == nil {
		return nil
	}
	if err := available(state); err != nil {
		state.Delivery = "paused"
		return nil
	}
	if state.Pending.MessageID == "" {
		state.Delivery = "pending_publication"
		return nil
	}
	if state.Activity == nil || state.Activity.Owner != state.Binding.Owner {
		state.Delivery = "unavailable"
		return nil
	}
	if state.Activity.Gate != Ready {
		state.Delivery = "deferred_" + string(state.Activity.Gate)
		return nil
	}
	plan := deliveryPlan(state.Pending, now)
	if plan != "submit" {
		state.Delivery = plan
		return nil
	}
	count := 1
	if state.Pending.Attempt != nil {
		count += state.Pending.Attempt.Count
	}
	state.Pending.Claim = nil
	state.Activity.Gate = Busy
	state.Pending.Attempt = &Attempt{
		ID:         rand.Text(),
		Generation: state.Binding.Generation,
		Phase:      "offered",
		Count:      count,
		At:         now.UTC(),
	}
	state.Delivery, state.LastError = "offered", ""
	result := *state.Pending
	return &result
}

func (store Store) OfferPull(
	ctx context.Context,
	generation string,
	now time.Time,
) (*Event, error) {
	var result *Event
	err := store.withBinding(ctx, generation, now, func(state *State) error {
		if state.Binding.Destination.Mode != Pull {
			return errors.New("delivery is not a registered Claude Stop consumer")
		}
		token, err := primary.CaptureProcessToken(os.Getpid())
		if err != nil {
			return err
		}
		if state.Binding.Consumer == nil || *state.Binding.Consumer != token {
			return errors.New("Stop consumer was replaced")
		}
		result = offerPull(state, now)
		return nil
	})
	return result, err
}
