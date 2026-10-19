package remoteworker

import (
	"context"
	"errors"
	"time"

	"github.com/roman/concierge/pkg/primary"
)

// Revoke requires the caller to revoke the primary grant under the task lock
// first. Persistent tunnel revocation prevents recovery during partial cleanup.
func (manager *TunnelManager) Revoke(ctx context.Context, owner SocketOwner) error {
	return manager.revoke(ctx, owner, requireTunnelRecord)
}

type revocationScope int

const (
	requireTunnelRecord revocationScope = iota
	unfinishedLaunch
)

// RevokePartialLaunch also accepts an absent record. Tunnel startup saves its
// record before creating a process. The caller excludes launch recovery and
// durably revokes the grant under the task lock before calling this method.
func (manager *TunnelManager) RevokePartialLaunch(ctx context.Context, owner SocketOwner) error {
	return manager.revoke(ctx, owner, unfinishedLaunch)
}

func (manager *TunnelManager) revoke(
	ctx context.Context,
	owner SocketOwner,
	scope revocationScope,
) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	return manager.store.WithTask(ctx, owner.Task, func(lease *TunnelLease) error {
		record, err := lease.Read()
		if err != nil {
			return err
		}
		if record == nil && scope == unfinishedLaunch {
			return nil
		}
		if record == nil || record.Spec.Owner != owner || record.Phase == TunnelStarting {
			return ErrTunnelOwnership
		}
		if record.Phase == TunnelClosed {
			return nil
		}
		if record.Phase != TunnelRevoked {
			record.Phase = TunnelRevoked
			if err := lease.Save(*record); err != nil {
				return err
			}
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
		if err := manager.remote.Prepare(ctx, record.Spec); err != nil {
			return errors.Join(ErrTunnelOwnership, err)
		}
		record.Phase, record.Process = TunnelClosed, nil
		record.Failures, record.NextRetry = 0, time.Time{}
		return lease.Save(*record)
	})
}
