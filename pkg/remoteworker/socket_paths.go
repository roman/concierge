package remoteworker

import (
	"fmt"
	"path/filepath"
	"strings"
)

const launchIdentityBytes = 16
const maxSocketPathBytes = 103

// ValidateSocketBase uses the fixed generation directory width, so readiness
// does not depend on a task or generation that has yet to be allocated.
func ValidateSocketBase(base string) error {
	directory := filepath.Join(base, "tasks", strings.Repeat("0", launchIdentityBytes*2))
	worker := filepath.Join(directory, "worker.sock")
	agent := filepath.Join(directory, "agent.sock")
	longest := max(len(worker), len(agent))
	if longest <= maxSocketPathBytes {
		return nil
	}
	maxBaseBytes := maxSocketPathBytes - (longest - len(base))
	return fmt.Errorf(
		"%w. Canonical base: %q. Worker socket: %d bytes. Agent socket: %d bytes. Maximum: %d bytes (Darwin). Use a shorter canonical base of at most %d bytes, such as /private/tmp/concierge.",
		ErrLaunchSocketPath,
		base,
		len(worker),
		len(agent),
		maxSocketPathBytes,
		maxBaseBytes,
	)
}
