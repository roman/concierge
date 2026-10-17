package conciergecli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type preparationMachines struct{ machine herdr.SavedMachine }

func (resolver preparationMachines) Resolve(context.Context, string) (herdr.SavedMachine, error) {
	return resolver.machine, nil
}

type preparationSSH struct {
	calls        int
	clientCommit string
}

func (runner *preparationSSH) Run(
	_ context.Context,
	arguments []string,
	_ io.Reader,
) ([]byte, []byte, error) {
	runner.calls++
	if arguments[0] == "-G" {
		return []byte(
			"hostname fake-target\nforwardagent no\nforwardx11 no\ntunnel false\n",
		), nil, nil
	}
	command := arguments[len(arguments)-1]
	if strings.Contains(command, "concierge-client-status") {
		return []byte(runner.clientCommit), nil, nil
	}
	if strings.Contains(command, "'uname' '-s'") {
		return []byte("Darwin"), nil, nil
	}
	if strings.Contains(command, "'uname' '-m'") {
		return []byte("arm64"), nil, nil
	}
	return []byte("version"), nil, nil
}

func TestMachineConfigurationBindsSavedIdentityAndDestination(t *testing.T) {
	valid := `{"machine-id":{"target":"baker","base":"/Users/roman/concierge","agent":"codex","repositories":[{"name":"concierge","url":"git@example.org:roman/concierge.git"}]}}`
	for _, test := range []struct {
		name, config string
		code         int
	}{
		{"configured", valid, 0},
		{"missing", "", 1},
		{"different identity", strings.ReplaceAll(valid, "machine-id", "other-id"), 1},
		{"different target", strings.ReplaceAll(valid, `"target":"baker"`, `"target":"other"`), 1},
		{"relative base", strings.ReplaceAll(valid, "/Users/roman/concierge", "relative"), 1},
		{"root base", strings.ReplaceAll(valid, "/Users/roman/concierge", "/"), 1},
		{"unknown field", strings.ReplaceAll(valid, `"agent":`, `"unknown":`), 1},
		{"trailing object", valid + ` {}`, 1},
		{"missing client repository", strings.ReplaceAll(valid, `"name":"concierge"`, `"name":"other"`), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.config != "" {
				if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "machines.json"), []byte(test.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &preparationSSH{}
			var output bytes.Buffer
			machine := herdr.SavedMachine{
				ID:      "machine-id",
				Label:   "baker",
				Target:  "baker",
				Session: "default",
				Enabled: true,
			}
			code := runMachineCheck(
				context.Background(),
				root,
				"baker",
				preparationMachines{machine},
				runner,
				&output,
			)
			if code != test.code {
				t.Fatalf("code %d: %s", code, &output)
			}
			if code != 0 && runner.calls != 0 {
				t.Fatal("invalid configuration reached SSH")
			}
			if code == 0 &&
				(!strings.Contains(output.String(), "status: ready") || !strings.Contains(output.String(), `machine_id: "machine-id"`)) {
				t.Fatal(output.String())
			}
			if code != 0 && !strings.Contains(output.String(), "machines.json") {
				t.Fatal("missing configuration repair step")
			}
		})
	}
}

func TestMachineCheckAndStatusHelp(t *testing.T) {
	for _, operation := range []string{"check", "status", "prepare"} {
		var output bytes.Buffer
		code := Run(
			context.Background(),
			[]string{"machine", operation, "--help"},
			nil,
			&output,
			io.Discard,
		)
		if code != 0 || !strings.Contains(output.String(), "<label>") ||
			!strings.Contains(
				output.String(),
				"machines.json",
			) || !strings.Contains(output.String(), `"repositories"`) {
			t.Fatalf("code %d: %s", code, &output)
		}
	}
}

func TestMachineStatusRequiresAConfirmedPreparedClient(t *testing.T) {
	for _, commit := range []string{"", strings.Repeat("a", 40)} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
			t.Fatal(err)
		}
		configuration := `{"machine-id":{"target":"baker","base":"/remote/base","agent":"codex","repositories":[{"name":"concierge","url":"ssh://git.test/concierge.git"}]}}`
		if err := os.WriteFile(filepath.Join(root, "config", "machines.json"), []byte(configuration), 0600); err != nil {
			t.Fatal(err)
		}
		machine := herdr.SavedMachine{
			ID:      "machine-id",
			Label:   "baker",
			Target:  "baker",
			Session: "default",
			Enabled: true,
		}
		var output bytes.Buffer
		code := runMachineStatus(
			context.Background(),
			root,
			"baker",
			preparationMachines{machine},
			&preparationSSH{clientCommit: commit},
			&output,
		)
		if commit == "" {
			if code == 0 || !strings.Contains(output.String(), "status: unconfirmed") {
				t.Fatalf("reported an unprepared client ready: %s", &output)
			}
		} else if code != 0 || !strings.Contains(output.String(), "client_commit: "+quote(commit)) {
			t.Fatalf("prepared status failed: %s", &output)
		}
	}
}
