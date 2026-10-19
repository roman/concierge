package remoteworker

import (
	"fmt"
	"runtime"
)

func ValidatePrimaryWorkerSocketPath(root, task string) error {
	return validatePrimaryWorkerSocketPath(root, task, runtime.GOOS)
}

func validatePrimaryWorkerSocketPath(root, task, platform string) error {
	limit := 107
	if platform == "darwin" {
		limit = 103
	}
	socket := PrimaryWorkerSocketPath(root, task)
	if len(socket) <= limit {
		return nil
	}
	return fmt.Errorf(
		"primary worker socket %q is %d bytes. Maximum: %d bytes (%s). Use a shorter primary --root of at most %d bytes. Retire existing state before changing its root",
		socket,
		len(socket),
		limit,
		platform,
		limit-(len(socket)-len(root)),
	)
}
