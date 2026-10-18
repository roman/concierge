package sshcommand

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStreamingSSHRetainsLargeBinaryOutputAndFiltersEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-forward")
	runner := SystemRunner{Executable: "/bin/sh"}
	payload := bytes.Repeat([]byte{0, 255, 1, 128}, 128<<10)
	var output bytes.Buffer
	diagnostics, err := runner.RunTo(context.Background(), []string{
		"-c", `test -z "${OPENAI_API_KEY:-}" || exit 1; cat; printf diagnostic >&2`,
	}, bytes.NewReader(payload), &output)
	if err != nil || !bytes.Equal(output.Bytes(), payload) || string(diagnostics) != "diagnostic" {
		t.Fatalf(
			"binary SSH transfer: bytes=%d diagnostics=%q err=%v",
			output.Len(),
			diagnostics,
			err,
		)
	}
	if _, _, err := runner.Run(context.Background(), []string{"-c", "cat"}, bytes.NewReader(payload)); !errors.Is(
		err,
		ErrOutputLimit,
	) {
		t.Fatalf("ordinary SSH response lost its bound: %v", err)
	}
}

type failedSSHOutput struct{}

func (failedSSHOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStreamingSSHReportsWriteFailureAndBoundsDiagnostics(t *testing.T) {
	runner := SystemRunner{Executable: "/bin/sh"}
	if _, err := runner.RunTo(context.Background(), []string{"-c", "printf bundle"}, nil, failedSSHOutput{}); err == nil {
		t.Fatal("reported a complete transfer after its destination failed")
	}
	if _, err := runner.RunTo(context.Background(), []string{"-c", "cat >&2"}, strings.NewReader(strings.Repeat("x", 300<<10)), io.Discard); !errors.Is(
		err,
		ErrOutputLimit,
	) {
		t.Fatalf("unbounded SSH diagnostics: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.RunTo(ctx, []string{"-c", "cat"}, nil, io.Discard); err == nil {
		t.Fatal("reported a complete cancelled transfer")
	}
}
