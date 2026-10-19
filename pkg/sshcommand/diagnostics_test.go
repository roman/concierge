package sshcommand

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreparationDiagnosticsStreamFullLogAndRetainFailureTail(t *testing.T) {
	var log bytes.Buffer
	runner := SystemRunner{Executable: "/bin/sh", Diagnostics: &log}
	payload := strings.Repeat("build progress\n", 30000) + "cache download failed\n"
	output, diagnostics, err := runner.Run(t.Context(),
		[]string{"-c", "cat >&2; exit 37"}, strings.NewReader(payload))
	if err == nil || err.Error() != "exit status 37" || len(output) != 0 {
		t.Fatalf("lost command status: %q %v", output, err)
	}
	if !strings.HasPrefix(log.String(), payload) ||
		!strings.Contains(log.String(), "exit status 37") {
		t.Fatal("full command diagnostics or completion status missing from host log")
	}
	if len(diagnostics) > 4096 || !bytes.HasSuffix(diagnostics, []byte("cache download failed\n")) {
		t.Fatalf("failure diagnostics are not a bounded tail: %d bytes", len(diagnostics))
	}
}
