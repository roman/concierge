package remoteworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

func TestTrustedTunnelPreparesAndProbesAgentSocket(t *testing.T) {
	_, spec := tunnelFixture(t)
	spec.Authentication = sshcommand.ForwardedAgent
	for _, operation := range []string{"prepare", "probe"} {
		status := "prepared"
		if operation == "probe" {
			status = "reachable"
		}
		runner := &secretRunner{stdout: []byte("status: " + status + "\n")}
		remote := SSHTunnelRemote{Runner: runner}
		perform := remote.Prepare
		if operation == "probe" {
			perform = remote.Probe
		}
		if err := perform(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(
			strings.Join(runner.arguments, " "),
			"'worker-socket' 'agent-"+operation+"'",
		) {
			t.Fatalf("trusted tunnel skipped agent %s: %v", operation, runner.arguments)
		}
	}
}

func TestSocketTransportUsesFixedHelperAndOwnership(t *testing.T) {
	_, owner := socketOwnerFixture(t)
	runner := &secretRunner{stdout: []byte("status: prepared\n")}
	profile := herdr.SavedMachine{ID: "machine-1", Target: "machine", Enabled: true}
	tools := Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}
	if err := (SocketTransport{Runner: runner}).Prepare(context.Background(), profile, tools, owner); err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(runner.arguments, " ")
	for _, required := range []string{
		"'worker-socket' 'prepare'", "'--directory' '/remote/task'", "'--task' 'task-1'",
		"'--generation' 'gen-1'", "'--verifier' '" + owner.Verifier + "'",
		"StrictHostKeyChecking=yes", "ForwardAgent=no", "ClearAllForwardings=yes",
	} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("missing helper argument %s", required)
		}
	}
	if len(runner.input) != 0 || runner.deadline.IsZero() ||
		time.Until(runner.deadline) > 15*time.Second {
		t.Fatal("socket helper sent input or has no bounded deadline")
	}
}

func TestSSHTunnelRemoteProbesThroughFixedNativeHelper(t *testing.T) {
	_, spec := tunnelFixture(t)
	runner := &secretRunner{stdout: []byte("status: reachable\n")}
	remote := SSHTunnelRemote{Runner: runner}
	if err := remote.Probe(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(runner.arguments, " "), "'worker-socket' 'probe'") ||
		len(runner.input) != 0 {
		t.Fatal("tunnel readiness did not use the fixed credential-free helper")
	}
	runner.failure = errors.New("SSH disconnected")
	if err := remote.Probe(context.Background(), spec); err == nil {
		t.Fatal("SSH failure accepted partial readiness output")
	}
}

func TestSocketTransportRetainsUnconfirmedOwnership(t *testing.T) {
	_, owner := socketOwnerFixture(t)
	profile := herdr.SavedMachine{ID: "machine-1", Target: "machine", Enabled: true}
	tools := Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}
	for _, test := range []struct {
		name   string
		stdout string
		stderr string
		err    error
	}{
		{name: "disconnect", stdout: "status: prepared\n", err: errors.New("disconnected")},
		{name: "warning", stdout: "status: prepared\n", stderr: "warning"},
		{name: "unknown response", stdout: "status: unknown\n"},
		{name: "active socket", stdout: "error: \"" + ErrSocketActive.Error() + "\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &secretRunner{
				stdout:  []byte(test.stdout),
				stderr:  []byte(test.stderr),
				failure: test.err,
			}
			err := (SocketTransport{Runner: runner}).Prepare(
				context.Background(),
				profile,
				tools,
				owner,
			)
			if err == nil {
				t.Fatal("uncertain socket preparation succeeded")
			}
			if test.name == "active socket" && !errors.Is(err, ErrSocketActive) {
				t.Fatalf("active listener status lost: %v", err)
			}
		})
	}
}
