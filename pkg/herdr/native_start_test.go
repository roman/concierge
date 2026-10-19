package herdr

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failedNativeStart struct {
	reads    atomic.Int32
	samples  atomic.Int32
	scenario string
}

func (runner *failedNativeStart) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	switch strings.Join(args[:2], " ") {
	case "agent start":
		<-ctx.Done()
		return nil, nil, ctx.Err()
	case "agent read":
		read := runner.reads.Add(1)
		if read == 1 && runner.scenario != "stale error" {
			return []byte("shell prompt"), nil, nil
		}
		return []byte(
			"Error adding directories: effective permissions reject /remote/task\nshell prompt",
		), nil, nil
	case "pane process-info":
		if runner.scenario == "unreachable" {
			return nil, nil, errors.New("unreachable")
		}
		if runner.scenario == "live" {
			return []byte(
				`{"result":{"type":"pane_process_info","process_info":{"pane_id":"p1","shell_pid":100,"foreground_processes":[{"pid":101,"name":"codex","argv0":"codex"}]}}}`,
			), nil, nil
		}
		runner.samples.Add(1)
		return []byte(
			`{"result":{"type":"pane_process_info","process_info":{"pane_id":"p1","shell_pid":100,"foreground_processes":[{"pid":100,"name":"zsh","argv0":"zsh"}]}}}`,
		), nil, nil
	}
	return nil, nil, errors.New("unexpected command")
}

func (runner *failedNativeStart) RunMachine(
	ctx context.Context,
	_ string,
	args ...string,
) ([]byte, []byte, error) {
	return runner.Run(ctx, args...)
}

func TestRemoteNativeStartupFailureStopsWaitingAndRetainsCause(t *testing.T) {
	runner := &failedNativeStart{}
	client := Client{Runner: runner, MachineRunner: runner, machine: "baker", session: "default"}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := client.runNativeStart(ctx, "p1", []string{"agent", "start", "worker"})
	if !errors.Is(err, ErrNativeStartup) ||
		!strings.Contains(err.Error(), "effective permissions reject /remote/task") ||
		ctx.Err() != nil {
		t.Fatalf("native failure remained hidden: %v", err)
	}
	if runner.samples.Load() < 2 {
		t.Fatal("startup failure did not require stable shell evidence")
	}
}

func TestRemoteNativeStartupRetainsUncertainOrLiveLaunches(t *testing.T) {
	for _, scenario := range []string{"unreachable", "live", "stale error"} {
		t.Run(scenario, func(t *testing.T) {
			runner := &failedNativeStart{scenario: scenario}
			client := Client{
				Runner:        runner,
				MachineRunner: runner,
				machine:       "baker",
				session:       "default",
			}
			ctx, cancel := context.WithTimeout(t.Context(), 700*time.Millisecond)
			defer cancel()
			if err := client.runNativeStart(ctx, "p1", []string{"agent", "start", "worker"}); !errors.Is(
				err,
				context.DeadlineExceeded,
			) ||
				errors.Is(err, ErrNativeStartup) {
				t.Fatalf("misclassified %s: %v", scenario, err)
			}
		})
	}
}
