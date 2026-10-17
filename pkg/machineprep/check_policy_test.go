package machineprep

import (
	"context"
	"io"
	"strings"
	"testing"
)

type unsafePolicyProbe struct{ remote *probeRunner }

func (runner unsafePolicyProbe) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	if arguments[0] == "-G" {
		return []byte(
			"hostname fake-target\nforwardagent yes\nforwardx11 no\ntunnel false\n",
		), nil, nil
	}
	return runner.remote.Run(ctx, arguments, input)
}

func TestMachineCheckReportsNativeSSHPolicyBeforePreparation(t *testing.T) {
	remote := &probeRunner{t: t}
	report, err := (Checker{Runner: unsafePolicyProbe{remote: remote}}).Check(
		context.Background(),
		testMachine(),
		"codex",
		[]Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready() {
		t.Fatal("reported a machine ready despite native credential forwarding")
	}
	for _, check := range report.Checks {
		if check.Name == "ssh-policy" && check.Status == "missing" &&
			strings.Contains(check.Step, "ForwardAgent no") &&
			len(remote.commands) == 1 {
			return
		}
	}
	t.Fatalf("missing policy repair step or continued probing after policy refusal: %+v", report)
}
