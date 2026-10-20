package heartbeat

import (
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

func TestPullOfferRequiresReadyCurrentHook(t *testing.T) {
	now := time.Unix(1000, 0)
	owner := primary.Identity{Kind: "claude", SessionID: "session", PID: 42}
	for _, trial := range []struct {
		name     string
		activity *Activity
		paused   bool
		kind     EventKind
		want     string
	}{
		{"missing", nil, false, Periodic, "unavailable"},
		{"busy", &Activity{Owner: owner, Gate: Busy}, false, Periodic, "deferred_busy"},
		{"permission", &Activity{Owner: owner, Gate: Permission}, false, Periodic, "deferred_permission"},
		{"unknown", &Activity{Owner: owner, Gate: Unknown}, false, Periodic, "deferred_unknown"},
		{"other owner", &Activity{Owner: primary.Identity{PID: 43}, Gate: Ready}, false, Periodic, "unavailable"},
		{"ready", &Activity{Owner: owner, Gate: Ready}, false, Periodic, "offered"},
		{"paused", &Activity{Owner: owner, Gate: Ready}, true, Periodic, "paused"},
		{"inbox during pause", &Activity{Owner: owner, Gate: Ready}, true, Inbox, "offered"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			state := State{
				Enabled:  true,
				Paused:   trial.paused,
				Activity: trial.activity,
				Binding:  &Binding{Owner: owner, Generation: "generation"},
				Pending:  &Event{ID: "event", Kind: trial.kind, MessageID: "published"},
			}
			got := offerPull(&state, now)
			if state.Delivery != trial.want || (got != nil) != (trial.want == "offered") {
				t.Fatalf("offer %+v, delivery %s, want %s", got, state.Delivery, trial.want)
			}
			if got != nil &&
				(got.Attempt.Count != 1 || state.Receipt != nil || state.Pending == nil) {
				t.Fatalf("offer consumed output: %+v", state)
			}
		})
	}
}

func TestPullRenewalRetainsRetryBudget(t *testing.T) {
	now := time.Now()
	owner := primary.Identity{Kind: "claude", SessionID: "session"}
	state := State{}
	destination := Destination{Mode: Pull, Session: "session", Expires: now.Add(time.Hour)}
	first := register(&state, owner, destination)
	state.Pending = &Event{ID: "event", Attempt: &Attempt{Count: 3, Phase: "offered"}}
	destination.Expires = now.Add(2 * time.Hour)
	renewed := register(&state, owner, destination)
	if renewed.Generation != first.Generation || state.Pending.Attempt == nil ||
		state.Pending.Attempt.Count != 3 {
		t.Fatal("renewal replaced event retry authority")
	}
	if renewed.Destination.Expires != destination.Expires.UTC() {
		t.Fatal("renewal did not extend the consumer expiry")
	}
	destination.Expires = now.Add(time.Minute)
	shorter := register(&state, owner, destination)
	if shorter.Generation != first.Generation ||
		shorter.Destination.Expires != renewed.Destination.Expires {
		t.Fatal("short renewal reduced existing authority")
	}
}

func TestPullOffersHaveBoundedRetries(t *testing.T) {
	now := time.Now()
	owner := primary.Identity{Kind: "claude", SessionID: "session"}
	state := State{
		Enabled:  true,
		Binding:  &Binding{Owner: owner, Generation: "generation"},
		Activity: &Activity{Owner: owner, Gate: Ready},
		Pending:  &Event{ID: "event", Kind: Periodic, MessageID: "published"},
	}
	for attempt := 1; attempt <= 3; attempt++ {
		event := offerPull(&state, now)
		if event == nil || event.Attempt.Count != attempt {
			t.Fatalf("attempt %d: %+v", attempt, state)
		}
		if offerPull(&state, now) != nil {
			t.Fatal("immediate duplicate offer")
		}
		now = now.Add(ClaimLease)
	}
	if offerPull(&state, now) != nil || state.Delivery != "retry_exhausted" ||
		state.Pending == nil {
		t.Fatalf("retry budget lost event: %+v", state)
	}
}
