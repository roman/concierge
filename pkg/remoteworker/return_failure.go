package remoteworker

import (
	"errors"
	"fmt"
)

type returnStage string

func (stage returnStage) Error() string { return "remote return " + string(stage) + " failed" }

const (
	ErrReturnRequest    returnStage = "request/protocol"
	ErrReturnJournal    returnStage = "journal/identity"
	ErrReturnProcesses  returnStage = "process checks"
	ErrReturnFsmonitor  returnStage = "fsmonitor shutdown"
	ErrReturnContents   returnStage = "content cleanup"
	ErrReturnPool       returnStage = "pool return"
	ErrReturnTransport  returnStage = "transport"
	ErrReturnValidation returnStage = "response validation"
)

func returnError(stage returnStage, cause error) error {
	if cause == nil {
		return stage
	}
	return fmt.Errorf("%w: %w", stage, cause)
}

func returnFailureStage(err error) returnStage {
	var stage returnStage
	if errors.As(err, &stage) {
		return stage
	}
	return ErrReturnJournal
}

func validReturnStage(stage returnStage) bool {
	switch stage {
	case ErrReturnRequest, ErrReturnJournal, ErrReturnProcesses, ErrReturnFsmonitor,
		ErrReturnContents, ErrReturnPool, ErrReturnTransport, ErrReturnValidation:
		return true
	}
	return false
}

// SetFailure sends fixed error categories to the caller and keeps bounded,
// redacted dependency details for the private host log.
func (response *WorktreeReturnResponse) SetFailure(err error) {
	stage := returnFailureStage(err)
	response.Failure = string(stage)
	response.Error = stage.Error()
	for _, known := range []error{ErrWorktreeOwnership, ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
		if errors.Is(err, known) {
			response.Error = known.Error()
			break
		}
	}
	response.Diagnostics = launchDiagnostics([]byte(err.Error()))
}

func (response WorktreeReturnResponse) failureCause() error {
	stage := returnStage(response.Failure)
	if !validReturnStage(stage) {
		return ErrReturnValidation
	}
	if response.Error == stage.Error() {
		return stage
	}
	if stage == ErrReturnJournal && response.Error == ErrWorktreeOwnership.Error() {
		return returnError(stage, ErrWorktreeOwnership)
	}
	if stage == ErrReturnProcesses {
		for _, known := range []error{ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
			if response.Error == known.Error() {
				return returnError(stage, known)
			}
		}
	}
	return ErrReturnValidation
}

func (transport WorktreeTransport) returnFailure(cause error, diagnostics []byte) error {
	if transport.Diagnostics != nil {
		_, _ = fmt.Fprintf(transport.Diagnostics, "remote_return: %s; stderr: %q\n",
			returnFailureStage(cause), launchDiagnostics(diagnostics))
	}
	return cause
}
