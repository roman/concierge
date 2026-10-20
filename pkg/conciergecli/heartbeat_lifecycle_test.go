package conciergecli

import (
	"context"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/heartbeat"
)

func TestHeartbeatActivationAndPauseCommands(t *testing.T) {
	root := taskRoot(t, t.TempDir())
	invoke(t, root, "", "heartbeat", "pause", "--reason", "maintenance")
	invoke(t, root, "", "heartbeat", "enable")
	state, err := (heartbeat.Store{Root: root}).Read(context.Background())
	if err != nil || !state.Enabled || !state.Paused {
		t.Fatalf("activation lost pause: %+v, %v", state, err)
	}
	invoke(t, root, "", "heartbeat", "resume")
	if got := invoke(t, root, "", "heartbeat", "show"); !strings.Contains(
		got,
		"operator_paused: no",
	) {
		t.Fatal(got)
	}
}

func TestHeartbeatCommandsReportOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"heartbeat", "show"}, {"heartbeat", "pause", "--reason", "maintenance"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := taskRoot(t, t.TempDir())
			if code := Run(context.Background(), append([]string{"--root", root}, args...), strings.NewReader(""), failedSettingsWriter{}, failedSettingsWriter{}); code != 1 {
				t.Fatalf("output failure returned %d", code)
			}
		})
	}
}
