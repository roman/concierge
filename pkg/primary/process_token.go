package primary

import (
	"errors"
	"os"
)

var ErrProcessOwnership = errors.New("process ownership cannot be confirmed")

type ProcessStatus string

const (
	ProcessRunning ProcessStatus = "running"
	ProcessExited  ProcessStatus = "exited"
)

// ProcessToken identifies one process incarnation across host restarts.
// A PID alone can refer to another process after the original process exits.
type ProcessToken struct {
	PID          int    `json:"pid"`
	StartTicks   string `json:"start_ticks"`
	BootID       string `json:"boot_id"`
	PIDNamespace string `json:"pid_namespace"`
}

func (token ProcessToken) Validate() error {
	if token.PID < 1 || token.StartTicks == "" || token.BootID == "" || token.PIDNamespace == "" {
		return ErrProcessOwnership
	}
	return nil
}

func CaptureProcessToken(pid int) (ProcessToken, error) {
	if pid < 1 {
		return ProcessToken{}, ErrProcessOwnership
	}
	boot, err := readBootID()
	if err != nil {
		return ProcessToken{}, ErrProcessOwnership
	}
	namespace, err := readPIDNamespace()
	if err != nil {
		return ProcessToken{}, ErrProcessOwnership
	}
	process, err := readProcess(pid)
	if err != nil || process.PID != pid {
		return ProcessToken{}, ErrProcessOwnership
	}
	token := ProcessToken{
		PID:          pid,
		StartTicks:   process.StartTicks,
		BootID:       boot,
		PIDNamespace: namespace,
	}
	return token, token.Validate()
}

func ProbeProcessToken(token ProcessToken) (ProcessStatus, error) {
	if err := token.Validate(); err != nil {
		return "", err
	}
	boot, err := readBootID()
	if err != nil {
		return "", ErrProcessOwnership
	}
	namespace, err := readPIDNamespace()
	if err != nil {
		return "", ErrProcessOwnership
	}
	return probeProcessToken(token, boot, namespace, readProcess)
}

func probeProcessToken(
	token ProcessToken,
	boot, namespace string,
	lookup func(int) (Process, error),
) (ProcessStatus, error) {
	if token.Validate() != nil || namespace == "" || boot == "" || namespace != token.PIDNamespace {
		return "", ErrProcessOwnership
	}
	if boot != token.BootID {
		return ProcessExited, nil
	}
	process, err := lookup(token.PID)
	if errors.Is(err, os.ErrNotExist) {
		return ProcessExited, nil
	}
	if err != nil || process.PID != token.PID || process.StartTicks == "" {
		return "", ErrProcessOwnership
	}
	if process.StartTicks != token.StartTicks {
		return ProcessExited, nil
	}
	return ProcessRunning, nil
}
