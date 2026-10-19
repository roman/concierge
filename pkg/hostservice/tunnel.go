package hostservice

import (
	"context"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

type taskTunnelProbe struct{ server *Server }

func (probe taskTunnelProbe) Probe(ctx context.Context, task, generation string) error {
	return probe.server.probeTaskTunnel(ctx, task, generation)
}

// SupervisedWorker holds the task lock throughout this read and recovery.
// Tunnel ownership cannot override a revoked or replaced task grant.
func (server *Server) probeTaskTunnel(ctx context.Context, taskID, generation string) error {
	current, err := server.store.Read(ctx, taskID)
	if err != nil {
		return err
	}
	snapshot := current.Snapshot()
	bound := snapshot.Worker
	if snapshot.State != taskstate.InFlight || snapshot.Completion != nil ||
		bound == nil || bound.Machine == "" || bound.PendingAdoption ||
		bound.PendingGeneration != "" || bound.Generation != generation ||
		bound.Access == nil || bound.Access.Revoked || bound.Access.Validate() != nil ||
		bound.Access.Task != taskID || bound.Access.Generation != generation {
		return remoteworker.ErrTunnelRevoked
	}
	if server.tunnels == nil {
		return remoteworker.ErrTunnelUnreachable
	}
	if err := server.ensureTaskWorkerSocket(taskID); err != nil {
		return err
	}
	return server.tunnels.Probe(ctx, remoteworker.SocketOwner{
		Task: taskID, Generation: generation, Verifier: bound.Access.Verifier,
	}, bound.Machine)
}
