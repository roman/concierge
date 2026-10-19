package remoteworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrTunnelUnreachable = errors.New("remote worker tunnel is unreachable")
var ErrTunnelRevoked = errors.New("remote worker tunnel access is revoked")

type TunnelProcesses interface {
	Start(context.Context, TunnelSpec, string) (primary.ProcessToken, error)
	Status(primary.ProcessToken) (primary.ProcessStatus, error)
	Stop(context.Context, primary.ProcessToken) error
}

type TunnelRemote interface {
	Prepare(context.Context, TunnelSpec) error
	Probe(context.Context, TunnelSpec) error
}

type TunnelManager struct {
	store       *TunnelStore
	localSocket string
	processes   TunnelProcesses
	remote      TunnelRemote
	Now         func() time.Time
}

func NewTunnelManager(
	root string,
	processes TunnelProcesses,
	remote TunnelRemote,
) (*TunnelManager, error) {
	store, err := NewTunnelStore(root)
	if err != nil {
		return nil, err
	}
	if processes == nil || remote == nil {
		return nil, errors.New("tunnel manager requires process and remote socket drivers")
	}
	return &TunnelManager{store: store, localSocket: filepath.Join(root, "worker.sock"),
		processes: processes, remote: remote, Now: time.Now}, nil
}

// Ensure requires the caller to hold the task lock and confirm its current grant.
// The tunnel record owns external resources; it never grants worker authority.
func (manager *TunnelManager) Ensure(ctx context.Context, spec TunnelSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	return manager.store.WithTask(ctx, spec.Owner.Task, func(lease *TunnelLease) error {
		record, err := lease.Read()
		if err != nil {
			return err
		}
		if record == nil || (record.Phase == TunnelClosed && record.Spec != spec) {
			record = &TunnelRecord{Format: 1, Spec: spec, Phase: TunnelRetry}
			if err := lease.Save(*record); err != nil {
				return err
			}
		}
		identity := record.Spec
		identity.PrimaryAgentSocket = spec.PrimaryAgentSocket
		if identity != spec {
			return ErrTunnelOwnership
		}
		return manager.reconcile(ctx, lease, record)
	})
}

// Probe matches the current grant and machine before recovering a tunnel.
// The caller confirms active access while holding the task's own lock.
func (manager *TunnelManager) Probe(
	ctx context.Context,
	owner SocketOwner,
	machineID string,
) error {
	if owner.Validate() != nil || machineID == "" {
		return ErrTunnelOwnership
	}
	return manager.store.WithTask(ctx, owner.Task, func(lease *TunnelLease) error {
		record, err := lease.Read()
		if err != nil {
			return err
		}
		if record == nil {
			return ErrTunnelUnreachable
		}
		if record.Spec.Owner != owner || record.Spec.MachineID != machineID {
			return ErrTunnelOwnership
		}
		return manager.reconcile(ctx, lease, record)
	})
}

func (manager *TunnelManager) reconcile(
	ctx context.Context,
	lease *TunnelLease,
	record *TunnelRecord,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch record.Phase {
	case TunnelRevoked, TunnelClosed:
		return ErrTunnelRevoked
	case TunnelStarting:
		// A crash between process creation and token publication leaves ownership
		// uncertain. A second process could create another listener.
		return ErrTunnelOwnership
	}
	if err := manager.refreshAgentDestination(ctx, lease, record); err != nil {
		return err
	}
	now := manager.Now().UTC()
	if now.Before(record.NextRetry) {
		return ErrTunnelUnreachable
	}
	if record.Process != nil {
		status, err := manager.processes.Status(*record.Process)
		if err != nil {
			return ErrTunnelOwnership
		}
		switch status {
		case primary.ProcessExited:
			record.Process, record.Phase = nil, TunnelRetry
			return tunnelRetry(lease, record, now)
		case primary.ProcessRunning:
		default:
			return ErrTunnelOwnership
		}
	} else {
		if err := manager.remote.Prepare(ctx, record.Spec); err != nil {
			if errors.Is(err, ErrSocketActive) || errors.Is(err, ErrSocketOwnership) {
				return ErrTunnelOwnership
			}
			return tunnelRetry(lease, record, now)
		}
		record.Phase = TunnelStarting
		if err := lease.Save(*record); err != nil {
			return err
		}
		process, err := manager.processes.Start(ctx, record.Spec, manager.localSocket)
		if errors.Is(err, ErrTunnelNotStarted) {
			record.Phase = TunnelRetry
			return errors.Join(tunnelRetry(lease, record, now), err)
		}
		if err != nil || process.Validate() != nil {
			return ErrTunnelOwnership
		}
		record.Process, record.Phase = &process, TunnelRunning
		if err := lease.Save(*record); err != nil {
			return err
		}
	}
	if err := manager.remote.Probe(ctx, record.Spec); err != nil {
		return tunnelRetry(lease, record, now)
	}
	if record.Failures == 0 {
		return nil
	}
	record.Failures, record.NextRetry = 0, time.Time{}
	return lease.Save(*record)
}

func (manager *TunnelManager) refreshAgentDestination(
	ctx context.Context,
	lease *TunnelLease,
	record *TunnelRecord,
) error {
	if record.Spec.Authentication != sshcommand.ForwardedAgent {
		return nil
	}
	desired := record.Spec
	desired.PrimaryAgentSocket = os.Getenv("SSH_AUTH_SOCK")
	if desired.PrimaryAgentSocket == "" || desired.Validate() != nil {
		return sshcommand.ErrPrimaryAgent
	}
	if desired == record.Spec {
		return nil
	}
	if record.Process != nil {
		status, err := manager.processes.Status(*record.Process)
		if err != nil {
			return ErrTunnelOwnership
		}
		if status == primary.ProcessRunning {
			if err := manager.processes.Stop(ctx, *record.Process); err != nil {
				return errors.Join(ErrTunnelOwnership, err)
			}
			status, err = manager.processes.Status(*record.Process)
		}
		if err != nil || status != primary.ProcessExited {
			return ErrTunnelOwnership
		}
	}
	// An old record can omit the destination. Replace only the token-owned
	// process before publishing the new destination and preparing its sockets.
	record.Process, record.Phase = nil, TunnelRetry
	record.Failures, record.NextRetry = 0, time.Time{}
	if err := lease.Save(*record); err != nil {
		return err
	}
	record.Spec = desired
	return lease.Save(*record)
}

func tunnelRetry(lease *TunnelLease, record *TunnelRecord, now time.Time) error {
	record.Failures = min(record.Failures+1, 63)
	delay := min(time.Second<<min(record.Failures-1, 6), time.Minute)
	record.NextRetry = now.Add(delay)
	if err := lease.Save(*record); err != nil {
		return err
	}
	return ErrTunnelUnreachable
}
