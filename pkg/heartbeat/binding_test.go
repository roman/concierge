package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

func TestBindingFencesReplacementAndExpires(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	owner := primary.Identity{Kind: "claude", SessionID: "session"}
	destination := Destination{Mode: Pull, Session: "session", Expires: now.Add(time.Hour)}
	var first Binding
	if err := store.update(ctx, func(state *State) error {
		first = register(state, owner, destination)
		if err := bindingCurrent(state, owner, first.Generation, now); err != nil {
			t.Fatal(err)
		}
		if err := bindingCurrent(state, owner, first.Generation, destination.Expires); err == nil {
			t.Fatal("expired hook accepted")
		}
		changed := owner
		changed.PID = 42
		if err := bindingCurrent(state, changed, first.Generation, now); err == nil {
			t.Fatal("replacement owner accepted")
		}
		state.Pending = &Event{ID: "pending", Claim: &Claim{Token: "old"}}
		replacement := register(state, changed, destination)
		if replacement.Generation == first.Generation || state.Pending.Claim != nil {
			t.Fatal("replacement retained old authority")
		}
		if err := bindingCurrent(state, changed, first.Generation, now); err == nil {
			t.Fatal("stale generation accepted")
		}
		if !state.Paused {
			t.Fatal("registration cleared the operator pause")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationRejectsAnotherNativeThread(t *testing.T) {
	thread := "019a0000-0000-7000-8000-000000000001"
	t.Setenv("CODEX_THREAD_ID", thread)
	if err := nativeDestination(primary.Identity{Kind: "codex"}, Destination{Thread: thread}); err != nil {
		t.Fatal(err)
	}
	if err := nativeDestination(primary.Identity{Kind: "codex"}, Destination{Thread: "019a0000-0000-7000-8000-000000000002"}); err == nil {
		t.Fatal("another thread accepted")
	}
	t.Setenv("CODEX_THREAD_ID", "")
	if err := nativeDestination(primary.Identity{Kind: "codex"}, Destination{Thread: thread}); err == nil {
		t.Fatal("missing native thread accepted")
	}
}

func TestBindingRetrySurvivesPersistence(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	owner := primary.Identity{Kind: "claude", SessionID: "owner"}
	destination := Destination{Mode: Pull, Session: "owner", Expires: time.Now().Add(time.Hour)}
	var first, retry Binding
	if err := store.update(ctx, func(state *State) error { first = register(state, owner, destination); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, func(state *State) error { retry = register(state, owner, destination); return nil }); err != nil {
		t.Fatal(err)
	}
	if first.Generation != retry.Generation {
		t.Fatal("identical retry rotated the persisted generation")
	}
}

func TestDestinationRejectsFieldsFromOtherMode(t *testing.T) {
	now := time.Now()
	token := primary.ProcessToken{PID: 1, StartTicks: "1", BootID: "boot", PIDNamespace: "ns"}
	for _, trial := range []struct {
		name   string
		mode   DeliveryMode
		change func(*Destination)
	}{
		{"pull server", Pull, func(d *Destination) { d.Server = token }},
		{"pull socket", Pull, func(d *Destination) { d.Socket = "/socket" }},
		{"pull thread", Pull, func(d *Destination) { d.Thread = "thread" }},
		{"push session", Push, func(d *Destination) { d.Session = "session" }},
		{"push expiry", Push, func(d *Destination) { d.Expires = now.Add(time.Hour) }},
	} {
		t.Run(trial.name, func(t *testing.T) {
			owner := primary.Identity{Kind: "claude", SessionID: "session"}
			destination := Destination{Mode: Pull, Session: "session", Expires: now.Add(time.Hour)}
			if trial.mode == Push {
				owner.Kind = "codex"
				destination = Destination{
					Mode:   Push,
					Socket: "/socket",
					Thread: "019a0000-0000-7000-8000-000000000001",
					Server: token,
				}
			}
			if err := destination.validate(owner, now); err != nil {
				t.Fatal(err)
			}
			trial.change(&destination)
			if err := destination.validate(owner, now); err == nil {
				t.Fatal("accepted field from other mode")
			}
		})
	}
}
