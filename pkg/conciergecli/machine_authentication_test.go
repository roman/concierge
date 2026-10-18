package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

func TestMachineCheckUsesConfiguredForwardingMode(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	configuration := `{"saved-id":{"target":"saved-target","base":"/remote/base","agent":"codex","authentication":"agent-forwarding","repositories":[{"name":"concierge","url":"git@example.org:owner/concierge.git"}]}}`
	if err := os.WriteFile(filepath.Join(root, "config", "machines.json"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &preparationSSH{}
	var output bytes.Buffer
	code := runMachineCheck(
		context.Background(),
		root,
		"baker",
		preparationMachines{
			herdr.SavedMachine{
				ID:      "saved-id",
				Target:  "saved-target",
				Session: "default",
				Enabled: true,
			},
		},
		runner,
		&output,
	)
	if code == 0 || runner.calls != 0 || !strings.Contains(output.String(), "primary-agent") {
		t.Fatalf(
			"missing primary agent did not prevent remote check: code=%d calls=%d %s",
			code,
			runner.calls,
			&output,
		)
	}
}
