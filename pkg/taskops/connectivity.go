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

func (worker *SupervisedWorker) observeRemote(
	ctx context.Context,
	task *taskstate.LockedTask,
	bound taskstate.Worker,
) error {
	snapshot := task.Current().Snapshot()
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil || bound.PendingAdoption {
		return taskstate.ErrInvalidTransition
	}
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
