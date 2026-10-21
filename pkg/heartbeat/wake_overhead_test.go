package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/roman/tincan/pkg/inbox"
)

func TestInboxOutputDefersPeriodicWakeButNotNewReplies(t *testing.T) {
	for _, test := range []struct {
		name     string
		after    time.Duration
		replies  []string
		want     EventKind
		interval string
	}{
		{name: "periodic inside grace", after: 59 * time.Second, replies: []string{"first"}},
		{name: "periodic at bound", after: time.Minute, replies: []string{"first"}, want: Periodic},
		{name: "new reply inside grace", after: time.Second, replies: []string{"first", "second"}, want: Inbox},
		{name: "longer interval preserved", after: time.Minute, replies: []string{"first"}, interval: "5m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := Store{Root: initializedRoot(t)}
			now := time.Unix(100, 0)
			if err := store.Enable(ctx, now); err != nil {
				t.Fatal(err)
			}
			policy := Settings{Interval: "1s"}
			if test.interval != "" {
				policy.Interval = test.interval
			}
			state, err := store.Schedule(
				ctx,
				now,
				Observation{Active: true, Text: "inbox", Replies: []string{"first"}},
				policy,
			)
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
			state, err = store.Schedule(
				ctx,
				now.Add(test.after),
				Observation{Active: true, Text: "changed", Replies: test.replies},
				policy,
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if state.Pending != nil {
					t.Fatalf("periodic wake inside grace: %+v", state.Pending)
				}
				return
			}
			if state.Pending == nil || state.Pending.Kind != test.want {
				t.Fatalf("pending = %+v, want %s", state.Pending, test.want)
			}
		})
	}
}

func TestPendingRepliesSharePublishedEvent(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	policy := Settings{Interval: "5m"}
	first, err := store.Schedule(
		ctx,
		now,
		Observation{Active: true, Replies: []string{"first"}},
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	messageRoot := t.TempDir()
	recipient, _ := inbox.ParseRecipient("concierge")
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	second, err := store.Schedule(
		ctx,
		now.Add(time.Second),
		Observation{Active: true, Replies: []string{"first", "second"}},
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Pending == nil || second.Pending.ID != first.Pending.ID ||
		second.Pending.Kind != Inbox {
		t.Fatalf("new reply duplicated pending event: %+v", second.Pending)
	}
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	messages, _ := inbox.New(messageRoot)
	pending, err := messages.List(recipient)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending messages = %v, %v", pending, err)
	}
}
