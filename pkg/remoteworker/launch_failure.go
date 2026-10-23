package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type launchStage string

func (stage launchStage) Error() string { return "remote launch " + string(stage) + " failed" }

const (
	ErrLaunchTransport   launchStage = "transport"
	ErrLaunchTimeout     launchStage = "timeout"
	ErrLaunchStderr      launchStage = "stderr"
	ErrLaunchDecode      launchStage = "response decoding"
	ErrLaunchValidation  launchStage = "response validation"
	ErrLaunchEnvironment launchStage = "environment inspection"
	ErrLaunchPreparation launchStage = "preparation"
)

type launchFailure struct {
	Stage       string `json:"launch_failure"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

func (failure launchFailure) cause() error {
	switch failure.Stage {
	case "directory ownership":
		return ErrLaunchDirectoryOwnership
	case "lease ownership":
		return ErrWorktreeOwnership
	case string(ErrLaunchTransport), string(ErrLaunchTimeout), string(ErrLaunchStderr),
		string(ErrLaunchDecode), string(ErrLaunchValidation), string(ErrLaunchEnvironment),
		string(ErrLaunchPreparation):
		return launchStage(failure.Stage)
	default:
		return ErrLaunchDecode
	}
}

// EncodeLaunchSiteFailure keeps remote failures separate from a successful site.
// Only a fixed stage and bounded stderr cross the wire; request and environment
// contents are never used as error details.
func EncodeLaunchSiteFailure(output io.Writer, err error) error {
	failure := launchFailure{Stage: launchFailureStage(err)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		failure.Diagnostics = launchDiagnostics(exit.Stderr)
	}
	return json.NewEncoder(output).Encode(failure)
}

func launchFailureStage(err error) string {
	var stage launchStage
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return string(ErrLaunchTimeout)
	case errors.As(err, &stage):
		return string(stage)
	case errors.Is(err, ErrLaunchDirectoryOwnership):
		return "directory ownership"
	case errors.Is(err, ErrWorktreeOwnership):
		return "lease ownership"
	default:
		return string(ErrLaunchPreparation)
	}
}

func launchDiagnostics(data []byte) string {
	const limit = 2 << 10
	text := string(data)
	// Redact at the source too: remote environment values are unavailable to
	// the host. Longest values go first to avoid exposing a suffix after a match.
	var values []string
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		sensitive := strings.Contains(strings.ToLower(key), "token") ||
			strings.Contains(strings.ToLower(key), "secret") ||
			strings.Contains(strings.ToLower(key), "password")
		if len(value) >= 4 || (sensitive && value != "") {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	truncated := len(text) > limit
	if truncated {
		text = text[:limit]
	}
	if truncated {
		text += " [truncated]"
	}
	return text
}

func (transport WorktreeTransport) launchFailure(cause error, diagnostics []byte) error {
	if transport.Diagnostics != nil {
		_, _ = fmt.Fprintf(transport.Diagnostics, "remote_launch: %s; stderr: %q\n",
			launchFailureStage(cause), launchDiagnostics(diagnostics))
	}
	return cause
}
