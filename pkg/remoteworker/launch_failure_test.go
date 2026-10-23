package remoteworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestLaunchFailureProtocolPreservesStages(t *testing.T) {
	for _, cause := range []error{ErrLaunchEnvironment, ErrLaunchDirectoryOwnership,
		ErrWorktreeOwnership, context.DeadlineExceeded, errors.New("private internal detail")} {
		t.Run(cause.Error(), func(t *testing.T) {
			var output bytes.Buffer
			if err := EncodeLaunchSiteFailure(&output, cause); err != nil {
				t.Fatal(err)
			}
			var failure launchFailure
			if err := decodeSetupRequest(&output, &failure); err != nil {
				t.Fatal(err)
			}
			want := cause
			if errors.Is(cause, context.DeadlineExceeded) {
				want = ErrLaunchTimeout
			} else if cause.Error() == "private internal detail" {
				want = ErrLaunchPreparation
			}
			if !errors.Is(failure.cause(), want) {
				t.Fatalf("lost stage: %+v", failure)
			}
		})
	}
}

func TestLaunchFailureDiagnosticsBoundAndRedactAtSource(t *testing.T) {
	secret := "test-private-environment-value"
	t.Setenv("LAUNCH_TEST_TOKEN", secret)
	stderr := "archive HTTP 404 " + strings.Repeat("x", 2020) + secret + strings.Repeat("z", 3000)
	var output bytes.Buffer
	err := fmt.Errorf("%w: %w", ErrLaunchEnvironment, &exec.ExitError{Stderr: []byte(stderr)})
	if err := EncodeLaunchSiteFailure(&output, err); err != nil {
		t.Fatal(err)
	}
	var failure launchFailure
	if err := decodeSetupRequest(&output, &failure); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(failure.Diagnostics, "test-") ||
		!strings.Contains(failure.Diagnostics, "archive HTTP 404") ||
		!strings.HasSuffix(failure.Diagnostics, " [truncated]") ||
		len(failure.Diagnostics) > (2<<10)+len(" [truncated]") {
		t.Fatalf("unsafe diagnostics: %q", failure.Diagnostics)
	}
}
