package conciergecli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/primary"
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

func TestHookOwnerRetriesOnlyTransientLockContention(t *testing.T) {
	for _, trial := range []struct {
		name  string
		first error
		want  error
		calls int
	}{
		{"busy then current", primary.ErrClaimBusy, nil, 2},
		{"unauthorized", primary.ErrUnauthorizedCaller, primary.ErrUnauthorizedCaller, 1},
	} {
		t.Run(trial.name, func(t *testing.T) {
			calls := 0
			err := retryHookOwner(context.Background(), func(context.Context) error {
				calls++
				if calls == 1 {
					return trial.first
				}
				return nil
			})
			if !errors.Is(err, trial.want) || calls != trial.calls {
				t.Fatalf("retry result %v after %d calls", err, calls)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := retryHookOwner(ctx, func(context.Context) error { return primary.ErrClaimBusy }); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("unbounded owner retry: %v", err)
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
