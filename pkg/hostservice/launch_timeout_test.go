package hostservice

import (
	"testing"
	"time"
)

func TestLaunchRequestAllowsColdDevshellStartup(t *testing.T) {
	for _, test := range []struct {
		operation Operation
		want      time.Duration
	}{
		{Launch, 23 * time.Minute},
		{PrepareMachine, 25 * time.Minute},
		{Intake, 2 * time.Minute},
	} {
		t.Run(string(test.operation), func(t *testing.T) {
			if got := requestTimeout(test.operation); got != test.want {
				t.Fatalf("request timeout: %s, want %s", got, test.want)
			}
		})
	}
}
