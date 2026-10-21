package heartbeat

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAcknowledgedStateSuppressesUnchangedReminders(t *testing.T) {
	for _, test := range []struct {
		name string
		kind EventKind
		text string
	}{
		{name: "periodic", kind: Periodic, text: "operator_actions: 1 open\nworker: running\n"},
		{name: "inbox", kind: Inbox, text: "operator_actions: 1 open\nworker: running\n"},
		{name: "empty snapshot", kind: Periodic},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := Store{Root: initializedRoot(t)}
			now := time.Unix(100, 0)
			if err := store.Enable(ctx, now); err != nil {
				t.Fatal(err)
			}
			observation := Observation{
				Active: true,
				Text:   test.text,
			}
			if test.kind == Inbox {
				observation.Replies = []string{"reply"}
			}
			policy := Settings{Interval: "1m", QuietLimit: 0}
			state, err := store.Schedule(ctx, now, observation, policy)
			if err != nil || state.Pending == nil {
				t.Fatalf("initial event missing: %+v, %v", state.Pending, err)
			}
			claim, err := store.Claim(ctx, state.Pending.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Acknowledge(ctx, state.Pending.ID, claim.Token, "posted", now); err != nil {
				t.Fatal(err)
			}
			for minute := 1; minute <= 3; minute++ {
				state, err = store.Schedule(
					ctx,
					now.Add(time.Duration(minute)*time.Minute),
					observation,
					policy,
				)
				if err != nil || state.Pending != nil {
					t.Fatalf("unchanged reminder %d: %+v, %v", minute, state.Pending, err)
				}
			}
			observation.Text = "operator_actions: 1 open\nworker: done\n"
			state, err = store.Schedule(ctx, now.Add(4*time.Minute), observation, policy)
			if err != nil || state.Pending == nil {
				t.Fatalf("changed state suppressed: %+v, %v", state, err)
			}
		})
	}
}

func TestLegacyInboxReceiptCannotSuppressChangedState(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, func(state *State) error {
		state.LastSnapshot = "older periodic state"
		state.QuietCount = 5
		state.Receipt = &Receipt{EventID: "inbox", Token: "claimed", Output: "different inbox state", At: now}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Schedule(
		ctx,
		now,
		Observation{Active: true, Text: "older periodic state"},
		Settings{Interval: "1m"},
	)
	if err != nil || state.Pending == nil {
		t.Fatalf("changed state suppressed by legacy receipt: %+v, %v", state, err)
	}
}

func TestLargeSnapshotCanBeAcknowledged(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	observation := Observation{Active: true, Text: strings.Repeat("x", maxStateBytes/2+1)}
	policy := Settings{Interval: "1m"}
	state, err := store.Schedule(ctx, now, observation, policy)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, state.Pending.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, state.Pending.ID, claim.Token, "posted", now); err != nil {
		t.Fatal(err)
	}
	state, err = store.Schedule(ctx, now.Add(time.Minute), observation, policy)
	if err != nil || state.Pending != nil {
		t.Fatalf("large unchanged snapshot: %+v, %v", state.Pending, err)
	}
}
