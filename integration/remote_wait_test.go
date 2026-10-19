package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
)

var errAcceptanceWaitTimeout = errors.New("acceptance wait timed out")

func acceptanceDecisionWait(value string) (time.Duration, error) {
	if value == "" {
		return time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Second || duration > 6*time.Hour {
		return 0, errors.New("--decision-wait requires a duration from 1s through 6h; default: 60m")
	}
	return duration, nil
}

func waitAcceptanceCondition(
	ctx context.Context,
	timeout, interval time.Duration,
	check func() (bool, error),
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, lastError := check()
		if ready && lastError == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.Join(errAcceptanceWaitTimeout, lastError)
		case <-ticker.C:
		}
	}
}

func nativeAcceptanceResumeHelp(machine, base string, wait time.Duration) string {
	command := "bash scripts/acceptance-remote.sh " + machine + " /absolute/path/new-report.md"
	if base != "" {
		command += " --remote-base " + base
	}
	return fmt.Sprintf(
		"Native decision wait ended. Owned cleanup starts now. The operator must answer native dialogs. After confirmed cleanup, select a new report path and rerun: %s --decision-wait %s",
		command,
		wait,
	)
}

func (run *savedMachineAcceptance) waitNative(id, name string, check func() (bool, error)) {
	previousScreen := ""
	err := waitAcceptanceCondition(run.ctx, run.decisionWait, time.Second, func() (bool, error) {
		ready, err := check()
		if ready && err == nil {
			return true, nil
		}
		client, pane, inspectErr := run.nativePane(run.ctx, id)
		if inspectErr != nil {
			return false, errors.Join(err, inspectErr)
		}
		agent, inspectErr := client.Get(run.ctx, pane)
		if inspectErr != nil {
			return false, errors.Join(err, inspectErr)
		}
		if agent.Status != herdr.Blocked {
			if previousScreen != "" {
				run.note("%s: native dialog cleared; native state %s", id, agent.Status)
			}
			previousScreen = ""
			return false, err
		}
		screen, inspectErr := client.VisibleScreen(run.ctx, pane)
		if inspectErr == nil && screen != previousScreen {
			run.note(
				"%s needs operator input: machine %s, pane %s; decision wait %s\n%s",
				id,
				run.machine,
				pane,
				run.decisionWait,
				screen,
			)
			previousScreen = screen
		}
		return false, errors.Join(err, inspectErr)
	})
	if err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if client, pane, inspectErr := run.nativePane(ctx, id); inspectErr == nil {
		if screen, inspectErr := client.VisibleScreen(ctx, pane); inspectErr == nil {
			run.note(
				"%s final native dialog or screen, machine %s pane %s:\n%s",
				id,
				run.machine,
				pane,
				screen,
			)
		}
	}
	run.note("%s", nativeAcceptanceResumeHelp(run.machine, run.fixedBase, run.decisionWait))
	run.t.Fatalf("%s after %s: %v", name, run.decisionWait, err)
}

func (run *savedMachineAcceptance) nativePane(
	ctx context.Context,
	id string,
) (herdr.Client, string, error) {
	task, err := run.store.Read(ctx, id)
	if err != nil {
		return herdr.Client{}, "", err
	}
	snapshot := task.Snapshot()
	machine, session, pane := "", "", ""
	if intent := snapshot.LaunchIntent; intent != nil && intent.Endpoint != nil {
		machine, session, pane = intent.Machine, intent.Session, intent.Endpoint.Pane
	} else if worker := snapshot.Worker; worker != nil {
		machine, session, pane = worker.Machine, worker.Session, worker.Pane
	}
	if pane == "" {
		return herdr.Client{}, "", errors.New("native pane is not recorded")
	}
	client, err := herdr.NewClient("herdr").ForEndpoint(machine, session)
	return client, pane, err
}

func TestAcceptanceDecisionWaitConfiguration(t *testing.T) {
	for _, test := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", time.Hour, false}, {"90m", 90 * time.Minute, false}, {"1s", time.Second, false},
		{"0", 0, true}, {"-1m", 0, true}, {"later", 0, true}, {"7h", 0, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			got, err := acceptanceDecisionWait(test.value)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("wait %q: %v %v", test.value, got, err)
			}
		})
	}
}

func TestAcceptanceWaitEndsOnSuccessDeadlineOrCancellation(t *testing.T) {
	if err := waitAcceptanceCondition(t.Context(), time.Second, time.Millisecond, func() (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	last := errors.New("operator decision pending")
	err := waitAcceptanceCondition(
		t.Context(),
		time.Millisecond,
		time.Millisecond,
		func() (bool, error) { return false, last },
	)
	if !errors.Is(err, errAcceptanceWaitTimeout) || !errors.Is(err, last) {
		t.Fatalf("timeout lost the cause: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitAcceptanceCondition(ctx, time.Hour, time.Second, func() (bool, error) { return false, nil }); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestNativeAcceptanceTimeoutGivesFixedBaseResumeCommand(t *testing.T) {
	message := nativeAcceptanceResumeHelp("baker", "/private/tmp/ca.RH2026", time.Hour)
	for _, part := range []string{"new report", "scripts/acceptance-remote.sh baker", "--remote-base /private/tmp/ca.RH2026", "--decision-wait 1h0m0s", "cleanup", "operator"} {
		if !strings.Contains(message, part) {
			t.Fatalf("missing %q: %s", part, message)
		}
	}
}
