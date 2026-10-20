package heartbeat

import (
	"os"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

func TestPullAvailabilityTracksConsumerProcess(t *testing.T) {
	now := time.Now()
	token, err := primary.CaptureProcessToken(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct {
		name      string
		consumer  *primary.ProcessToken
		pending   *Event
		wantError bool
	}{
		{"missing", nil, nil, true},
		{"live", &token, nil, false},
		{"replaced", &primary.ProcessToken{PID: token.PID, StartTicks: "other", BootID: token.BootID, PIDNamespace: token.PIDNamespace}, nil, true},
		{"offered", nil, &Event{Attempt: &Attempt{Phase: "offered", At: now}}, false},
		{"expired offer", nil, &Event{Attempt: &Attempt{Phase: "offered", At: now.Add(-ClaimLease)}}, true},
		{"processing", nil, &Event{Claim: &Claim{Until: now.Add(time.Minute)}}, false},
	} {
		t.Run(trial.name, func(t *testing.T) {
			state := State{Binding: &Binding{Consumer: trial.consumer}, Pending: trial.pending}
			if err := pullConsumerAvailable(&state, now); (err != nil) != trial.wantError {
				t.Fatalf("availability: %v", err)
			}
		})
	}
}
