package sshcommand

import (
	"bytes"
	"errors"
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

func TestSSHOutputLimitRetainsBoundedDiagnostics(t *testing.T) {
	for _, command := range []string{"cat >&2", "printf 'remote failure' >&2; cat"} {
		t.Run(command, func(t *testing.T) {
			runner := SystemRunner{Executable: "/bin/sh"}
			_, diagnostics, err := runner.Run(t.Context(), []string{"-c", command},
				strings.NewReader(strings.Repeat("remote failure\n", 30000)))
			if !errors.Is(err, ErrOutputLimit) || len(diagnostics) == 0 ||
				len(
					diagnostics,
				) > 256<<10 || !bytes.HasPrefix(diagnostics, []byte("remote failure")) {
				t.Fatalf("lost bounded failure diagnostics: bytes=%d err=%v", len(diagnostics), err)
			}
		})
	}
}
