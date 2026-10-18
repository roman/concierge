package remoteworker

import (
	"errors"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrTunnelOwnership = errors.New(
	"tunnel ownership is uncertain; retain resources and inspect its record",
)

type TunnelPhase string

const (
	TunnelStarting TunnelPhase = "starting"
	TunnelRunning  TunnelPhase = "running"
	TunnelRetry    TunnelPhase = "retry"
	TunnelRevoked  TunnelPhase = "revoked"
	TunnelClosed   TunnelPhase = "closed"
)

type TunnelSpec struct {
	Owner          SocketOwner               `json:"owner"`
	MachineID      string                    `json:"machine_id"`
	Target         string                    `json:"target"`
	Authentication sshcommand.Authentication `json:"authentication,omitempty"`
	Tools          Tools                     `json:"tools"`
}

func (spec TunnelSpec) Validate() error {
	if spec.Owner.Validate() != nil || spec.MachineID == "" ||
		!sshcommand.ValidTarget(
			spec.Target,
		) || spec.Tools.Validate() != nil || spec.Authentication.Validate() != nil {
		return ErrTunnelOwnership
	}
	return nil
}

type TunnelRecord struct {
	Format    int                   `json:"format"`
	Spec      TunnelSpec            `json:"spec"`
	Phase     TunnelPhase           `json:"phase"`
	Process   *primary.ProcessToken `json:"process,omitempty"`
	Failures  int                   `json:"failures,omitempty"`
	NextRetry time.Time             `json:"next_retry,omitempty"`
}

func (record TunnelRecord) Validate() error {
	if record.Format != 1 || record.Spec.Validate() != nil ||
		record.Failures < 0 || record.Failures > 63 ||
		(record.Failures == 0) != record.NextRetry.IsZero() {
		return ErrTunnelOwnership
	}
	if record.Process != nil && record.Process.Validate() != nil {
		return ErrTunnelOwnership
	}
	switch record.Phase {
	case TunnelRunning:
		if record.Process == nil {
			return ErrTunnelOwnership
		}
	case TunnelStarting, TunnelRetry, TunnelClosed:
		if record.Process != nil {
			return ErrTunnelOwnership
		}
	case TunnelRevoked:
	default:
		return ErrTunnelOwnership
	}
	return nil
}
