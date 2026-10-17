package taskops

import (
	"context"
	"errors"
	"testing"

	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteMissingRequiresReachableHerdrAndTunnel(t *testing.T) {
	for _, channel := range []string{"reachable", "unreachable tunnel"} {
		t.Run(channel, func(t *testing.T) {
			service, input, runner, original := remoteObservedFixture(t)
			runner.delegate.(*remoteAdoptionRunner).runner.absent = true
			probe := &fakeTunnelProbe{task: input.TaskID, generation: input.Generation}
			if channel == "unreachable tunnel" {
				probe.failure = errors.New("SSH tunnel unavailable")
			}
			worker := original.WithTunnelProbe(probe)
			err := worker.Observe(context.Background())
			if channel == "reachable" {
				if !errors.Is(err, supervise.ErrMissing) ||
					errors.Is(err, supervise.ErrUnreachable) {
					t.Fatalf("confirmed absence: %v", err)
				}
			} else if !errors.Is(err, supervise.ErrUnreachable) || errors.Is(err, supervise.ErrMissing) {
				t.Fatalf("tunnel partition permitted replacement: %v", err)
			}
			if channel == "unreachable tunnel" {
				err := worker.Observe(context.Background())
				if !errors.Is(err, supervise.ErrUnreachable) ||
					errors.Is(err, supervise.ErrMissing) ||
					probe.calls != 1 {
					t.Fatalf("tunnel backoff permitted replacement or another probe: %v", err)
				}
			}
			current, err := service.store.Read(context.Background(), input.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			bound := current.Snapshot().Worker
			if bound.Generation != input.Generation || bound.Worktree != input.Worktree ||
				bound.Connections.Herdr.Status != taskstate.ConnectionReachable ||
				probe.calls != 1 {
				t.Fatal("absence changed identity or lost independent reachability evidence")
			}
		})
	}
}
