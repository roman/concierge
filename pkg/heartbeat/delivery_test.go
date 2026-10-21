package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotificationCarriesExactClaimCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CONCIERGE_HOME", "")
	t.Setenv("TINCAN_HOME", "")
	notification := Notification(
		"/bin/concierge",
		"/operator's/.concierge",
		"/messages",
		"concierge",
		Event{ID: "event-1"},
		"generation-1",
	)
	command := `'/bin/concierge' --root '/operator'"'"'s/.concierge' --message-root '/messages' --recipient 'concierge' heartbeat claim 'event-1' --generation 'generation-1' --runtime host`
	if !strings.Contains(notification, "Run "+command+".") {
		t.Fatalf("missing exact claim command: %s", notification)
	}
}

func TestNotificationUsesAdoptedShell(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "concierge")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	for _, trial := range []struct {
		name, root, messages, want string
	}{
		{"adopted", "/state", "/messages", "concierge heartbeat claim 'event' --generation 'gen' --runtime host"},
		{"unset", "", "", "concierge --root '/state' --message-root '/messages' --recipient 'concierge' heartbeat claim"},
		{"different", "/other", "/other-messages", "concierge --root '/state' --message-root '/messages' --recipient 'concierge' heartbeat claim"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			t.Setenv("CONCIERGE_HOME", trial.root)
			t.Setenv("TINCAN_HOME", trial.messages)
			got := Notification(
				executable,
				"/state",
				"/messages",
				"concierge",
				Event{ID: "event"},
				"gen",
			)
			if !strings.Contains(got, "Run "+trial.want) {
				t.Fatal(got)
			}
			ack := AcknowledgementCommand(
				executable,
				"/state",
				"/messages",
				"concierge",
				Event{ID: "event", Claim: &Claim{Token: "operator's-token"}},
				"gen",
			)
			want := "concierge"
			if trial.name != "adopted" {
				want += " --root '/state' --message-root '/messages' --recipient 'concierge'"
			}
			want += ` heartbeat ack 'event' --generation 'gen' --runtime host --token 'operator'"'"'s-token' --output OUTPUT_REFERENCE`
			if ack != want {
				t.Fatal(ack)
			}
		})
	}
}

func TestNotificationPromptBudget(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "concierge"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	root := "/home/roman/Projects/self/exmachina/.concierge"
	messages := "/home/roman/Projects/self/exmachina/.tincan"
	executable := "/nix/store/lba5rhby3qnaqxjr2sv1mixyvl5pwpfq-concierge-0.1.0/bin/.concierge-wrapped"
	for _, trial := range []struct {
		name, root, messages string
		limit                int
	}{
		{"adopted", root, messages, 700},
		{"fallback", "", "", 900},
	} {
		t.Run(trial.name, func(t *testing.T) {
			t.Setenv("CONCIERGE_HOME", trial.root)
			t.Setenv("TINCAN_HOME", trial.messages)
			got := Notification(
				executable,
				root,
				messages,
				"concierge",
				Event{ID: strings.Repeat("E", 26)},
				strings.Repeat("G", 26),
			)
			t.Logf("prompt bytes: %d", len(got))
			if len(got) > trial.limit {
				t.Fatalf("prompt exceeds %d bytes: %d", trial.limit, len(got))
			}
		})
	}
}

func TestNotificationExplainsNativeSandboxRecovery(t *testing.T) {
	notification := Notification(
		"/bin/concierge",
		"/private/.concierge",
		"/private/.tincan",
		"concierge",
		Event{ID: "event"},
		"generation",
	)
	for _, instruction := range []string{
		"normal native approval flow",
		"sandbox_permissions=require_escalated",
		"Preserve the configured permissions, trust, owner, and binding",
	} {
		if !strings.Contains(notification, instruction) {
			t.Fatalf("missing sandbox recovery instruction %q: %s", instruction, notification)
		}
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
