package hostservice

import (
	"context"
	"fmt"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/primary"
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
	lease, err := hostactivity.Quiesce(server.config.Root)
	if err != nil {
		return Response{Outcome: Busy, Error: err.Error()}
	}
	tasks, err := server.store.List(ctx)
	if err == nil {
		for _, task := range tasks {
			snapshot := task.Snapshot()
			worker := snapshot.Worker
			if snapshot.LaunchIntent != nil ||
				(worker != nil && (worker.PendingGeneration != "" || worker.PendingLaunch != "" || worker.PendingAdoption)) {
				err = fmt.Errorf(
					"launch for task %s is in progress; finish or abandon it before restarting the host",
					task.ID(),
				)
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
