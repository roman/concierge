package integration

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/hostactivity"
)

func acceptanceRestartBusy(output string, commandError error) bool {
	if commandError == nil {
		return false
	}
	busyError := "error: " + strconv.Quote(hostactivity.ErrBusy.Error())
	for _, line := range strings.Split(output, "\n") {
		if line == busyError {
			return true
		}
	}
	return false
}

func TestAcceptanceRestartRecognizesOnlyConfirmedBusyRefusal(t *testing.T) {
	failure := errors.New("exit status 1")
	for _, trial := range []struct {
		name, output string
		err          error
		busy         bool
	}{
		{"busy refusal", `error: "` + hostactivity.ErrBusy.Error() + `"` + "\nhelp: inspect the host", failure, true},
		{"completed restart", "status: restarted", nil, false},
		{"successful output mentions activity", `error: "` + hostactivity.ErrBusy.Error() + `"`, nil, false},
		{"ambiguous timeout", `error: "context deadline exceeded"`, failure, false},
		{"busy phrase outside the error", `error: "replacement failed"` + "\nhelp: " + hostactivity.ErrBusy.Error(), failure, false},
		{"extended error", `error: "` + hostactivity.ErrBusy.Error() + `; shutdown outcome unknown"`, failure, false},
	} {
		t.Run(trial.name, func(t *testing.T) {
			if actual := acceptanceRestartBusy(trial.output, trial.err); actual != trial.busy {
				t.Fatalf("restart refusal recognized=%v, want %v", actual, trial.busy)
			}
		})
	}
}
