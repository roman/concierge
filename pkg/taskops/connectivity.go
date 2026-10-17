package taskops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskstate"
)

const remoteProbeTimeout = 10 * time.Second

// TunnelProbe checks one persisted task tunnel without acquiring the task lock.
// The caller holds that lock throughout the probe and records its outcome.
type TunnelProbe interface {
	Probe(context.Context, string, string) error
}

func (worker *SupervisedWorker) WithTunnelProbe(probe TunnelProbe) *SupervisedWorker {
	configured := *worker
	configured.tunnelProbe = probe
	return &configured
}

func (worker *SupervisedWorker) observeRemote(
	ctx context.Context,
	task *taskstate.LockedTask,
	bound taskstate.Worker,
) error {
	snapshot := task.Current().Snapshot()
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil || bound.PendingAdoption {
		return taskstate.ErrInvalidTransition
	}
	herdrError := worker.observeRemoteHerdr(ctx, task, bound)
	if herdrError != nil && !errors.Is(herdrError, supervise.ErrUnreachable) &&
		!errors.Is(herdrError, supervise.ErrMissing) {
		return herdrError
	}
	if worker.tunnelProbe == nil {
		return herdrError
	}
	if bound.Connections != nil && bound.Connections.Tunnel.Waiting(worker.now()) {
		if errors.Is(herdrError, supervise.ErrMissing) {
			return supervise.ErrUnreachable
		}
		return errors.Join(herdrError, supervise.ErrUnreachable)
	}
	probe, cancel := context.WithTimeout(ctx, remoteProbeTimeout)
	defer cancel()
	tunnelError := worker.tunnelProbe.Probe(probe, worker.taskID, bound.Generation)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if tunnelError == nil {
		tunnelError = probe.Err()
	}
	tunnelError = worker.recordConnection(task, taskstate.TunnelChannel, tunnelError)
	if tunnelError != nil && !errors.Is(tunnelError, supervise.ErrUnreachable) {
		return tunnelError
	}
	if errors.Is(herdrError, supervise.ErrMissing) && tunnelError != nil {
		return tunnelError
	}
	return errors.Join(herdrError, tunnelError)
}

func (worker *SupervisedWorker) observeRemoteHerdr(
	ctx context.Context,
	task *taskstate.LockedTask,
	bound taskstate.Worker,
) error {
	if bound.Connections != nil && bound.Connections.Herdr.Waiting(worker.now()) {
		return supervise.ErrUnreachable
	}
	if bound.PendingGeneration != "" {
		return worker.recordConnection(task, taskstate.HerdrChannel, ErrRemoteLaunchUnavailable)
	}
	probe, cancel := context.WithTimeout(ctx, remoteProbeTimeout)
	defer cancel()
	err := worker.observeEndpoint(probe, task, bound)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, taskstate.ErrPublicationUnconfirmed) {
		return err
	}
	if errors.Is(err, supervise.ErrMissing) && probe.Err() == nil {
		if err := worker.recordConnection(task, taskstate.HerdrChannel, nil); err != nil {
			return err
		}
		return supervise.ErrMissing
	}
	if err == nil {
		err = probe.Err()
	}
	return worker.recordConnection(task, taskstate.HerdrChannel, err)
}

func (worker *SupervisedWorker) recordConnection(
	task *taskstate.LockedTask,
	channel taskstate.ConnectionChannel,
	failure error,
) error {
	message := ""
	if failure != nil {
		message = strings.Join(strings.Fields(diagnosticText(failure.Error())), " ")
		if message == "" {
			message = "remote probe failed"
		}
	}
	generation := task.Current().Snapshot().Worker.Generation
	if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) {
		return current.RecordConnection(generation, channel, taskstate.ConnectionObservation{At: worker.now(), Error: message})
	}); err != nil {
		return err
	}
	if failure != nil {
		return fmt.Errorf("%w: %s", supervise.ErrUnreachable, message)
	}
	return nil
}
