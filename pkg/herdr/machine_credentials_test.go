package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const safeMachinePolicy = "hostname fake-target\nforwardagent no\nforwardx11 no\ntunnel false\n"

func machineCredentialFixture(t *testing.T, policy string) string {
	t.Helper()
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "policy")
	if err := os.WriteFile(policyPath, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	ssh := "#!/bin/sh\n[ \"$1\" = -G ] || exit 89\ncat '" + policyPath + "'\n"
	if err := os.WriteFile(filepath.Join(directory, "ssh"), []byte(ssh), 0700); err != nil {
		t.Fatal(err)
	}
	herdrScript := `#!/bin/sh
if [ "$1" = machine ] && [ "$2" = list ]; then
    printf '%s\n' '[{"id":"saved-id","label":"dev-remote","target":"fake-target","session":"default","enabled":true}]'
    exit 0
fi
printf 'native-call\n'
printf 'api-key=%s\n' "${OPENAI_API_KEY-unset}"
printf 'cloud-key=%s\n' "${AWS_SECRET_ACCESS_KEY-unset}"
printf 'config=%s\n' "${HERDR_CONFIG_PATH-unset}"
printf 'state=%s\n' "${XDG_STATE_HOME-unset}"
`
	executable := filepath.Join(directory, "herdr")
	if err := os.WriteFile(executable, []byte(herdrScript), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	return executable
}

func TestMachineSubprocessDropsPrimaryKeysAndKeepsCatalogPaths(t *testing.T) {
	executable := machineCredentialFixture(t, safeMachinePolicy)
	t.Setenv("OPENAI_API_KEY", "dummy-primary-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "dummy-cloud-key")
	t.Setenv("HERDR_CONFIG_PATH", "/explicit/herdr.toml")
	t.Setenv("XDG_STATE_HOME", "/explicit/state")
	client, err := NewClient(executable).ForMachine("saved-id")
	if err != nil {
		t.Fatal(err)
	}
	output, _, err := client.Runner.Run(context.Background(), "pane", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "dummy-") ||
		!strings.Contains(string(output), "api-key=unset") ||
		!strings.Contains(string(output), "config=/explicit/herdr.toml") ||
		!strings.Contains(string(output), "state=/explicit/state") {
		t.Fatal("remote Herdr inherited primary credentials or lost its catalog paths")
	}
}

func TestMachineSubprocessRefusesConfiguredForwardingBeforeNativeCall(t *testing.T) {
	for _, policy := range []string{strings.ReplaceAll(safeMachinePolicy, "forwardagent no", "forwardagent yes"), safeMachinePolicy + "remoteforward /remote/host.sock /primary/host.sock\n"} {
		t.Run(policy, func(t *testing.T) {
			executable := machineCredentialFixture(t, policy)
			client, err := NewClient(executable).ForMachine("saved-id")
			if err != nil {
				t.Fatal(err)
			}
			output, _, err := client.Runner.Run(context.Background(), "pane", "list")
			if err == nil || len(output) != 0 {
				t.Fatal("unsafe native SSH configuration reached the remote command")
			}
		})
	}
}

func TestRemotePresentationCheckDropsPrimaryCredentials(t *testing.T) {
	executable := machineCredentialFixture(t, safeMachinePolicy)
	t.Setenv("OPENAI_API_KEY", "dummy-primary-key")
	ssh := filepath.Join(filepath.Dir(executable), "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nprintf 'api-key=%s' \"${OPENAI_API_KEY-unset}\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output, _, err := NewClient(
		executable,
	).RemoteChecks.RunCheck(
		context.Background(),
		"saved-id",
		"default",
		SessionInventory,
	)
	if err != nil || string(output) != "api-key=unset" {
		t.Fatal("remote presentation check inherited primary credentials")
	}
}
