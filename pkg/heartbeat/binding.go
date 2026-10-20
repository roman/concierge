package heartbeat

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

type DeliveryMode string

const (
	Push DeliveryMode = "codex_queue"
	Pull DeliveryMode = "claude_stop"
)

type Destination struct {
	Mode    DeliveryMode         `json:"mode"`
	Socket  string               `json:"socket,omitempty"`
	Thread  string               `json:"thread,omitempty"`
	Server  primary.ProcessToken `json:"server,omitempty"`
	Session string               `json:"session,omitempty"`
	Expires time.Time            `json:"expires,omitempty"`
}

type Binding struct {
	Consumer    *primary.ProcessToken `json:"consumer,omitempty"`
	Owner       primary.Identity      `json:"owner"`
	Generation  string                `json:"generation"`
	Destination Destination           `json:"destination"`
}

var threadUUID = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

func (destination Destination) validate(owner primary.Identity, now time.Time) error {
	switch destination.Mode {
	case Push:
		if owner.Kind != "codex" || !threadUUID.MatchString(destination.Thread) ||
			!filepath.IsAbs(destination.Socket) ||
			filepath.Clean(destination.Socket) != destination.Socket ||
			destination.Session != "" ||
			!destination.Expires.IsZero() {
			return errors.New(
				"Codex delivery requires an explicit clean socket path and thread UUID",
			)
		}
		return destination.Server.Validate()
	case Pull:
		if owner.Kind != "claude" || destination.Session != owner.SessionID ||
			!now.Before(destination.Expires) ||
			destination.Expires.After(now.Add(8*time.Hour)) ||
			destination.Socket != "" ||
			destination.Thread != "" || destination.Server != (primary.ProcessToken{}) {
			return errors.New(
				"Claude delivery requires the owning session and a hook expiry within eight hours",
			)
		}
		return nil
	default:
		return errors.New("unknown supervisor delivery mode")
	}
}

func register(state *State, owner primary.Identity, destination Destination) Binding {
	destination.Expires = destination.Expires.UTC()
	if state.Binding != nil && state.Binding.Owner == owner && destination.Mode == Pull &&
		state.Binding.Destination.Mode == Pull && state.Binding.Destination.Session == destination.Session {
		if state.Binding.Destination.Expires.Before(destination.Expires) {
			state.Binding.Destination.Expires = destination.Expires
		}
		return *state.Binding
	}
	if state.Binding != nil && state.Binding.Owner == owner &&
		state.Binding.Destination == destination {
		return *state.Binding
	}
	binding := Binding{Owner: owner, Generation: rand.Text(), Destination: destination}
	state.Binding = &binding
	if state.Pending != nil {
		state.Pending.Claim = nil
		state.Pending.Attempt = nil
	}
	return binding
}

// Register runs verify while the current owner is fenced. A push adapter must
// verify its socket peer and selected thread before this record becomes usable.
func (store Store) Register(
	ctx context.Context,
	destination Destination,
	verify func(primary.Identity) error,
	now time.Time,
) (Binding, error) {
	var result Binding
	if verify == nil {
		return result, errors.New("native destination verification is required")
	}
	err := primary.WithCallerOwner(
		store.Root,
		primary.Caller{PID: os.Getpid(), UID: uint32(os.Geteuid())},
		func(owner primary.Identity) error {
			if err := destination.validate(owner, now); err != nil {
				return err
			}
			if err := nativeDestination(owner, destination); err != nil {
				return err
			}
			if err := verify(owner); err != nil {
				return err
			}
			var consumer *primary.ProcessToken
			if destination.Mode == Pull {
				token, err := primary.CaptureProcessToken(os.Getpid())
				if err != nil {
					return err
				}
				consumer = &token
			}
			return store.update(
				ctx,
				func(state *State) error {
					result = register(state, owner, destination)
					result.Consumer, state.Binding.Consumer = consumer, consumer
					return nil
				},
			)
		},
	)
	return result, err
}

func bindingCurrent(state *State, owner primary.Identity, generation string, now time.Time) error {
	if state.Binding == nil {
		return errors.New("primary delivery is unavailable: destination missing")
	}
	binding := state.Binding
	if generation == "" || binding.Generation != generation || binding.Owner != owner {
		return errors.New("stale primary delivery generation or owner")
	}
	if binding.Destination.Mode == Pull && !now.Before(binding.Destination.Expires) {
		return errors.New("primary delivery is unavailable: Stop hook expired")
	}
	return nil
}

// CheckBinding reports current availability without changing a stale record.
func (store Store) CheckBinding(ctx context.Context, generation string, now time.Time) error {
	return primary.WithOwner(store.Root, func(owner primary.Identity) error {
		state, err := store.Read(ctx)
		if err != nil {
			return err
		}
		if err := bindingCurrent(&state, owner, generation, now); err != nil {
			return err
		}
		if state.Binding.Destination.Mode == Pull {
			if err := pullConsumerAvailable(&state, now); err != nil {
				return err
			}
		}
		return store.verifyDestination(ctx, state.Binding.Destination)
	})
}

func nativeDestination(owner primary.Identity, destination Destination) error {
	if owner.Kind == "claude" && os.Getenv("CLAUDE_CODE_SESSION_ID") != destination.Session {
		return errors.New("destination differs from the inherited native Claude session")
	}
	if owner.Kind == "codex" &&
		(os.Getenv("CODEX_THREAD_ID") == "" || os.Getenv("CODEX_THREAD_ID") != destination.Thread) {
		return errors.New("destination differs from the inherited native Codex thread UUID")
	}
	return nil
}

func (store Store) withBinding(
	ctx context.Context,
	generation string,
	now time.Time,
	action func(*State) error,
) error {
	return primary.WithCallerOwner(
		store.Root,
		primary.Caller{PID: os.Getpid(), UID: uint32(os.Geteuid())},
		func(owner primary.Identity) error {
			return store.update(ctx, func(state *State) error {
				if err := bindingCurrent(state, owner, generation, now); err != nil {
					return err
				}
				if err := nativeDestination(owner, state.Binding.Destination); err != nil {
					return err
				}
				if err := store.verifyDestination(ctx, state.Binding.Destination); err != nil {
					return err
				}
				return action(state)
			})
		},
	)
}
