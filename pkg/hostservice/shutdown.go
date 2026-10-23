package hostservice

import (
	"context"
	"fmt"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/taskstate"
)

const HostStop Operation = "host_stop"

func (server *Server) stopResponse(
	ctx context.Context,
	caller primary.Caller,
	request Request,
) Response {
	if err := authorizeStatusCaller(caller); err != nil {
		return Response{Outcome: Unauthorized, Error: err.Error()}
	}
	if err := server.checkExpectations(request.Expectations); err != nil {
		return Response{Outcome: Invalid, Error: err.Error()}
	}
	server.shutdownMu.Lock()
	defer server.shutdownMu.Unlock()
	if server.stopping {
		return Response{Outcome: Busy, Error: "host shutdown is in progress"}
	}
	resume, err := server.controllers.suspendInitialRecovery(ctx)
	if err != nil {
		return Response{Outcome: Busy, Error: err.Error()}
	}
	defer func() {
		if !server.stopping {
			resume()
		}
	}()
	lease, err := hostactivity.Quiesce(server.config.Root)
	if err != nil {
		return Response{Outcome: Busy, Error: err.Error()}
	}
	tasks, err := server.store.List(ctx)
	if err == nil {
		for _, task := range tasks {
			if err = server.checkShutdownTask(ctx, task.ID()); err != nil {
				break
			}
		}
	}
	if err != nil {
		lease.Close()
		return Response{Outcome: Busy, Error: err.Error()}
	}
	server.stopping, server.shutdownLease = true, lease
	return Response{Outcome: Completed, Status: server.status()}
}

func (server *Server) checkShutdownTask(ctx context.Context, taskID string) error {
	_, err := server.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
		snapshot := locked.Current().Snapshot()
		worker := snapshot.Worker
		if worker != nil && (snapshot.LaunchIntent != nil || worker.PendingGeneration != "" ||
			worker.PendingLaunch != "" || worker.PendingAdoption) {
			return fmt.Errorf(
				"launch for task %s is in progress; finish or abandon it before restarting the host",
				taskID,
			)
		}
		intent := snapshot.LaunchIntent
		if intent == nil {
			return nil
		}
		client, err := herdr.NewClient(server.config.HerdrExecutable).
			ForEndpoint(intent.Machine, intent.Session)
		if err != nil {
			return err
		}
		resources := herdr.LaunchResources{Name: intent.Name, Label: intent.Label}
		if intent.Endpoint != nil {
			resources.Tab, resources.Pane = intent.Endpoint.Tab, intent.Endpoint.Pane
		}
		if err := client.CheckLaunchAbsent(ctx, resources); err != nil {
			return fmt.Errorf("launch for task %s: %w", taskID, err)
		}
		return nil
	})
	return err
}
