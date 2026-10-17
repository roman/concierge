package remoteworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/workeraccess"
)

type secretRunner struct {
	arguments []string
	input     []byte
	stdout    []byte
	stderr    []byte
	failure   error
	deadline  time.Time
}

func (runner *secretRunner) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	runner.arguments = arguments
	runner.deadline, _ = ctx.Deadline()
	if input != nil {
		runner.input, _ = io.ReadAll(input)
	}
	return runner.stdout, runner.stderr, runner.failure
}

func TestSecretTransportUsesStdinAndRemoteClientPaths(t *testing.T) {
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	runner := &secretRunner{stdout: []byte("status: installed\n")}
	transport := SecretTransport{Runner: runner}
	profile := herdr.SavedMachine{
		ID:      "machine-1",
		Target:  "user@machine",
		Session: "work",
		Enabled: true,
	}
	tools := Tools{Concierge: "/remote/client", TaskDirectory: "/remote/tasks/task one"}
	if err := transport.Install(context.Background(), profile, tools, secret); err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(runner.arguments, " ")
	if string(runner.input) != string(secret.Bytes()) ||
		strings.Contains(arguments, string(secret.Bytes())) ||
		!strings.Contains(
			arguments,
			"'/remote/client' 'worker-secret' 'install' '--directory' '/remote/tasks/task one'",
		) {
		t.Fatal("secret delivery did not keep content on stdin or used the wrong remote paths")
	}
	if runner.deadline.IsZero() || time.Until(runner.deadline) > 15*time.Second {
		t.Fatal("secret delivery has no bounded deadline")
	}
	runner.stdout, runner.input = []byte("status: removed\n"), nil
	if err := transport.Remove(context.Background(), profile, tools, grant.Verifier); err != nil {
		t.Fatal(err)
	}
	if len(runner.input) != 0 ||
		!strings.Contains(
			strings.Join(runner.arguments, " "),
			"'--verifier' '"+grant.Verifier+"'",
		) {
		t.Fatal("secret removal did not bind its verifier")
	}
}

func TestSecretTransportFailureNeverExposesSecretOrAcceptsPartialOutput(t *testing.T) {
	_, secret, _ := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	for _, failure := range []struct {
		stdout string
		stderr string
		err    error
	}{
		{"status: installed\n", "", errors.New("SSH failed")},
		{"status: installed\n", string(secret.Bytes()), nil},
		{string(secret.Bytes()), "", nil},
	} {
		runner := &secretRunner{
			stdout:  []byte(failure.stdout),
			stderr:  []byte(failure.stderr),
			failure: failure.err,
		}
		err := (SecretTransport{Runner: runner}).Install(context.Background(),
			herdr.SavedMachine{ID: "machine-1", Target: "machine", Enabled: true},
			Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}, secret)
		if err == nil || strings.Contains(err.Error(), string(secret.Bytes())) {
			t.Fatal("ambiguous secret delivery succeeded or exposed credential content")
		}
	}
}

func TestSecretTransportRejectsDisabledOrUnsafeMachineBeforeSSH(t *testing.T) {
	_, secret, _ := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	for _, profile := range []herdr.SavedMachine{
		{ID: "machine-1", Target: "machine"},
		{ID: "machine-1", Target: "-oProxyCommand=bad", Enabled: true},
		{Target: "machine", Enabled: true},
	} {
		runner := &secretRunner{}
		err := (SecretTransport{Runner: runner}).Install(context.Background(), profile,
			Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}, secret)
		if err == nil || runner.arguments != nil {
			t.Fatal("invalid machine reached SSH")
		}
	}
}
