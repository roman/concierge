package remoteworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/replyoutbox"
)

func TestReplyContextTransportUsesOnlyLaunchMetadata(t *testing.T) {
	_, spec := tunnelFixture(t)
	binding := replyoutbox.Context{Format: 1, Root: "/primary/state", Recipient: "concierge",
		Task: spec.Owner.Task, Generation: spec.Owner.Generation, Verifier: spec.Owner.Verifier}
	runner := &secretRunner{stdout: []byte("status: bound\n")}
	transport := ContextTransport{Runner: runner}
	profile := herdr.SavedMachine{ID: spec.MachineID, Target: spec.Target, Enabled: true}
	if err := transport.Install(context.Background(), profile, spec.Tools, binding); err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(runner.arguments, " ")
	if len(runner.input) != 0 || !strings.Contains(arguments, "'worker-context' 'install'") ||
		!strings.Contains(arguments, "'--state-root' '/primary/state'") ||
		!strings.Contains(
			arguments,
			"'--primary-recipient' 'concierge'",
		) || runner.deadline.IsZero() {
		t.Fatal("context transport changed its metadata or omitted its deadline")
	}
	runner.failure = errors.New("SSH disconnected")
	if err := transport.Install(context.Background(), profile, spec.Tools, binding); err == nil {
		t.Fatal("context transport accepted partial output after disconnect")
	}
}
