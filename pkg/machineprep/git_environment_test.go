package machineprep

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

type gitCredentialProbe struct {
	probes      *probeRunner
	environment []string
	failure     *string
}

func (runner gitCredentialProbe) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	commandText := arguments[len(arguments)-1]
	if !strings.Contains(commandText, "'ls-remote'") {
		return runner.probes.Run(ctx, arguments, input)
	}
	command := exec.CommandContext(ctx, "sh", "-c", commandText)
	command.Env = runner.environment
	output, err := command.Output()
	if failure, ok := err.(*exec.ExitError); ok {
		if runner.failure != nil {
			*runner.failure = string(failure.Stderr)
		}
		return output, failure.Stderr, err
	}
	return output, nil, err
}

func TestRepositoryReadCheckDoesNotForwardRemoteModelCredentials(t *testing.T) {
	directory := t.TempDir()
	log := filepath.Join(directory, "environment")
	script := "#!/bin/sh\nprintf '%s\\n' \"${OPENAI_API_KEY-unset}\" >> " + sshcommand.Quote(
		log,
	) + "\nprintf '0000'\n"
	if err := os.WriteFile(filepath.Join(directory, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	environment := append(
		sshcommand.ConnectionEnvironment(os.Environ()),
		"PATH="+directory+":"+os.Getenv("PATH"),
		"OPENAI_API_KEY=fixture-model-key",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	var failure string
	report, err := (Checker{Runner: gitCredentialProbe{probes: &probeRunner{t: t}, environment: environment, failure: &failure}}).Check(
		context.Background(),
		testMachine(),
		"codex",
		[]Repository{{Name: "project", URL: "ssh://fixture/repository"}},
	)
	if err != nil || !report.Ready() {
		t.Fatalf("repository fixture failed: %+v %v; fixture stderr: %s", report, err, failure)
	}
	data, err := os.ReadFile(log)
	if err != nil || len(data) == 0 || strings.Contains(string(data), "fixture-model-key") {
		t.Fatal("repository SSH inherited remote model credentials")
	}
}
