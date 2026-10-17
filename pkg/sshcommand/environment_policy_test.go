package sshcommand

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchSuppressesConfiguredCredentialEnvironment(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("system SSH is unavailable")
	}
	configuration := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configuration, []byte("Host fixture\n  HostName example.invalid\n  SetEnv OPENAI_API_KEY=fixture-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	arguments, err := TrustedBatchArguments("fixture", "true")
	if err != nil {
		t.Fatal(err)
	}
	arguments = append([]string{"-G", "-F", configuration}, arguments[:len(arguments)-1]...)
	output, _, err := (SystemRunner{Executable: ssh}).Run(context.Background(), arguments, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "fixture-only") ||
		!strings.Contains(string(output), "setenv CONCIERGE_SSH_TRANSPORT=1") {
		t.Fatal("batch SSH retains configured credential values")
	}
}
