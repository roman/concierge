package heartbeat

import (
	"testing"

	"github.com/roman/concierge/pkg/primary"
)

func TestCodexStartupBindsOnlyItsNativeHook(t *testing.T) {
	owner := primary.Identity{
		Version:      2,
		Kind:         "codex",
		SessionID:    "019a0000-0000-7000-8000-000000000001",
		PID:          42,
		StartTicks:   "100",
		BootID:       "boot",
		PIDNamespace: "namespace",
	}
	destination := Destination{
		Mode:   Push,
		Thread: owner.SessionID,
		Socket: "/private/native.sock",
		Server: primary.ProcessToken{
			PID:          owner.PID,
			StartTicks:   owner.StartTicks,
			BootID:       owner.BootID,
			PIDNamespace: owner.PIDNamespace,
		},
	}
	for _, trial := range []struct {
		name      string
		change    func(*primary.Identity, *Destination)
		wantError bool
	}{
		{"current native hook", func(*primary.Identity, *Destination) {}, false},
		{"other session", func(h *primary.Identity, _ *Destination) { h.SessionID = "other" }, true},
		{"other thread", func(_ *primary.Identity, d *Destination) { d.Thread = "other" }, true},
		{"other server", func(_ *primary.Identity, d *Destination) { d.Server.PID++ }, true},
		{"reused pid", func(_ *primary.Identity, d *Destination) { d.Server.StartTicks = "200" }, true},
		{"other boot", func(_ *primary.Identity, d *Destination) { d.Server.BootID = "other" }, true},
		{"other namespace", func(_ *primary.Identity, d *Destination) { d.Server.PIDNamespace = "other" }, true},
		{"Claude hook", func(h *primary.Identity, _ *Destination) { h.Kind = "claude" }, true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			hook, candidate := owner, destination
			trial.change(&hook, &candidate)
			if err := validateCodexHook(owner, hook, candidate); (err != nil) != trial.wantError {
				t.Fatalf("validation = %v, want error %t", err, trial.wantError)
			}
		})
	}
}
