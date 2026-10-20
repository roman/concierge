package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/roman/tincan/pkg/inbox"
)

func TestActivationPreservesAnEarlierOperatorPause(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	if err := store.Pause(ctx, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := store.Enable(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err := store.Read(ctx)
	if err != nil || !state.Paused || state.PauseReason != "maintenance" {
		t.Fatalf("activation cleared operator pause: %+v, %v", state, err)
	}
}

func TestScheduleCoalescesAndCountsAcknowledgedOutput(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	policy := Settings{Interval: "1m", QuietLimit: 1}
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	observation := Observation{Active: true, Text: "working"}
	first, err := store.Schedule(ctx, now, observation, policy)
	if err != nil || first.Pending == nil {
		t.Fatalf("not scheduled: %+v, %v", first, err)
	}
	retry, err := store.Schedule(ctx, now.Add(time.Hour), observation, policy)
	if err != nil || retry.Pending.ID != first.Pending.ID || retry.QuietCount != 0 {
		t.Fatalf("missed periods duplicated or consumed quiet count: %+v, %v", retry, err)
	}
	acknowledge := func(id string, at time.Time) {
		t.Helper()
		claim, err := store.Claim(ctx, id, at)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Acknowledge(ctx, id, claim.Token, "posted:"+id, at); err != nil {
			t.Fatal(err)
		}
	}
	acknowledge(first.Pending.ID, now)
	second, err := store.Schedule(ctx, now.Add(time.Hour), observation, policy)
	if err != nil || second.Pending == nil {
		t.Fatalf("not scheduled: %+v, %v", second, err)
	}
	acknowledge(second.Pending.ID, now.Add(time.Hour))
	quiet, err := store.Schedule(ctx, now.Add(2*time.Hour), observation, policy)
	if err != nil || quiet.Pending != nil || quiet.QuietCount != 1 {
		t.Fatalf("quiet contract: %+v, %v", quiet, err)
	}
	changed, err := store.Schedule(
		ctx,
		now.Add(3*time.Hour),
		Observation{Active: true, Text: "finished"},
		policy,
	)
	if err != nil || changed.Pending == nil {
		t.Fatalf("changed state suppressed: %+v, %v", changed, err)
	}
	acknowledge(changed.Pending.ID, now.Add(3*time.Hour))
	complete, err := store.Schedule(ctx, now.Add(4*time.Hour), Observation{}, policy)
	if err != nil || complete.Pending != nil {
		t.Fatalf("completed work scheduled: %+v, %v", complete, err)
	}
}

func TestNewReplyRemainsEligibleDuringPeriodicPause(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, func(state *State) error {
		state.SeenReplies = []string{"old"}
		state.Next = now.Add(time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, "operator maintenance"); err != nil {
		t.Fatal(err)
	}
	policy := Settings{Interval: "5m", QuietLimit: 5}
	quiet, err := store.Schedule(
		ctx,
		now,
		Observation{Active: true, Text: "old", Replies: []string{"old"}},
		policy,
	)
	if err != nil || quiet.Pending != nil {
		t.Fatalf("old reply woke: %+v, %v", quiet, err)
	}
	current, err := store.Schedule(
		ctx,
		now,
		Observation{Active: true, Text: "new", Replies: []string{"old", "new"}},
		policy,
	)
	if err != nil || current.Pending == nil || current.Pending.Kind != Inbox {
		t.Fatalf("new reply blocked: %+v, %v", current, err)
	}
	claim, err := store.Claim(ctx, current.Pending.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, "still paused"); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, current.Pending.ID, claim.Token, "reply posted", now); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationIsStableAndDoesNotAcknowledgeOutput(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	event, err := store.Stage(ctx, now, "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	messageRoot := t.TempDir()
	recipient, _ := inbox.ParseRecipient("concierge")
	for range 2 {
		if err := store.Publish(ctx, messageRoot, recipient); err != nil {
			t.Fatal(err)
		}
	}
	messages, _ := inbox.New(messageRoot)
	pending, err := messages.List(recipient)
	if err != nil || len(pending) != 1 {
		t.Fatalf("publication duplicated: %v, %v", pending, err)
	}
	state, err := store.Read(ctx)
	if err != nil || state.Pending == nil || state.Receipt != nil {
		t.Fatalf("publication closed output: %+v, %v", state, err)
	}
	claim, err := store.Claim(ctx, event.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "posted", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, messageRoot, recipient); err != nil {
		t.Fatal(err)
	}
	state, err = store.Read(ctx)
	if err != nil || state.Receipt.MessageID != "" {
		t.Fatalf("completed mailbox cleanup retained: %+v, %v", state, err)
	}
	pending, err = messages.List(recipient)
	if err != nil || len(pending) != 0 {
		t.Fatalf("receipt not reconciled: %v, %v", pending, err)
	}
}

func TestNewReplyCannotReclassifyActivePeriodicClaim(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	event, err := store.Stage(ctx, now, "periodic", nil)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, event.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Schedule(
		ctx,
		now,
		Observation{Active: true, Replies: []string{"new"}},
		Settings{Interval: "1m", QuietLimit: 1},
	)
	if err != nil || state.Pending.Kind != Periodic {
		t.Fatalf("active periodic claim reclassified: %+v, %v", state, err)
	}
	if err := store.Pause(ctx, "pause"); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "old periodic output", now); err == nil {
		t.Fatal("periodic output acknowledged during pause")
	}
	state, err = store.Schedule(
		ctx,
		now,
		Observation{Active: true, Replies: []string{"new"}},
		Settings{Interval: "1m", QuietLimit: 1},
	)
	if err != nil || state.Pending.Kind != Inbox {
		t.Fatalf("new reply lost after periodic claim revoked: %+v, %v", state, err)
	}
}

func TestInboxOutputPreservesPeriodicQuietLimit(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, func(state *State) error { state.LastSnapshot = "unchanged"; state.QuietCount = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	observation := Observation{Active: true, Text: "unchanged", Replies: []string{"new"}}
	policy := Settings{Interval: "1m", QuietLimit: 1}
	state, err := store.Schedule(ctx, now, observation, policy)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, state.Pending.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, state.Pending.ID, claim.Token, "inbox output", now); err != nil {
		t.Fatal(err)
	}
	state, err = store.Schedule(ctx, now.Add(time.Minute), observation, policy)
	if err != nil || state.Pending != nil || state.QuietCount != 1 {
		t.Fatalf("inbox output resumed quiet periodic work: %+v, %v", state, err)
	}
}
