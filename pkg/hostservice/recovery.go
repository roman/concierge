package hostservice

import "fmt"

// RecoveryPolicy selects automatic task recovery for the host lifetime.
type RecoveryPolicy string

const (
	RecoveryNormal      RecoveryPolicy = "normal"
	RecoveryMaintenance RecoveryPolicy = "maintenance"
)

// Effective treats an omitted policy from an older host as normal recovery.
func (policy RecoveryPolicy) Effective() RecoveryPolicy {
	if policy == "" {
		return RecoveryNormal
	}
	return policy
}

func (policy RecoveryPolicy) validate() error {
	switch policy.Effective() {
	case RecoveryNormal, RecoveryMaintenance:
		return nil
	default:
		return fmt.Errorf("invalid host recovery policy %q; use normal or maintenance", policy)
	}
}
