package heartbeat

import (
	"strings"
	"testing"
	"time"
)

func TestNotificationCarriesExactClaimCommand(t *testing.T) {
	notification := Notification(
		"/bin/concierge",
		"/operator's/.concierge",
		"/messages",
		"concierge",
		Event{ID: "event-1"},
		"generation-1",
	)
	command := `'/bin/concierge' --root '/operator'"'"'s/.concierge' --message-root '/messages' --recipient 'concierge' heartbeat claim 'event-1' --generation 'generation-1'`
	if !strings.Contains(notification, "Run "+command+".") {
		t.Fatalf("missing exact claim command: %s", notification)
	}
}

func TestDeliveryBudgetAndUncertainty(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, trial := range []struct {
		name  string
		event Event
		want  string
	}{
		{"new", Event{}, "submit"},
		{"processing", Event{Claim: &Claim{Until: now.Add(time.Minute)}}, "processing"},
		{"accepted", Event{Attempt: &Attempt{Phase: "accepted", Count: 1, At: now}}, "accepted"},
		{"retry accepted", Event{Attempt: &Attempt{Phase: "accepted", Count: 1, At: now.Add(-ClaimLease)}}, "submit"},
		{"uncertain", Event{Attempt: &Attempt{Phase: "uncertain", Count: 1, At: now}}, "reconcile"},
		{"crash during submit", Event{Attempt: &Attempt{Phase: "submitting", Count: 1, At: now}}, "reconcile"},
		{"retry exhausted", Event{Attempt: &Attempt{Phase: "accepted", Count: 3, At: now.Add(-ClaimLease)}}, "retry_exhausted"},
		{"failed backoff", Event{Attempt: &Attempt{Phase: "failed", Count: 1, At: now}}, "backoff"},
		{"invalid count", Event{Attempt: &Attempt{Phase: "failed", Count: -1, At: now}}, "unavailable"},
		{"unknown phase", Event{Attempt: &Attempt{Phase: "unknown", Count: 1, At: now}}, "unavailable"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			if got := deliveryPlan(&trial.event, now); got != trial.want {
				t.Fatalf("plan %s, want %s", got, trial.want)
			}
		})
	}
}
