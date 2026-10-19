package sshcommand

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBatchArgumentsQuoteCommandsAndDisableForwarding(t *testing.T) {
	args, err := TrustedBatchArguments(
		"user@machine",
		"/remote/client",
		"worker-secret",
		"install",
		"--directory",
		"/remote/task one's",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range []string{"BatchMode=yes", "ExitOnForwardFailure=yes", "ForwardAgent=no", "ForwardX11=no", "ForwardX11Trusted=no", "ClearAllForwardings=yes", "StrictHostKeyChecking=yes", "UpdateHostKeys=no"} {
		if !slices.Contains(args, option) {
			t.Fatalf("missing fixed SSH option %s", option)
		}
	}
	if args[len(args)-2] != "user@machine" ||
		args[len(args)-1] != "'/remote/client' 'worker-secret' 'install' '--directory' '/remote/task one'\"'\"'s'" {
		t.Fatalf("unsafe remote command: %q", args)
	}
	for _, target := range []string{"", "-oProxyCommand=bad", "host\ncommand", "host command"} {
		if _, err := BatchArguments(target, "true"); err == nil {
			t.Fatalf("unsafe SSH target accepted: %q", target)
		}
	}
}

type rejectingWriter struct{}

func (rejectingWriter) Write([]byte) (int, error) { return 0, ErrOutputLimit }

func TestStreamingRunnerStopsProducerOnRejectedOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := (SystemRunner{Executable: "/bin/sh"}).RunTo(ctx,
		[]string{"-c", "while :; do printf 'unbounded output'; done"}, nil, rejectingWriter{})
	if !errors.Is(err, ErrOutputLimit) || ctx.Err() != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("producer did not stop on overflow: %v (context: %v)", err, ctx.Err())
	}
}

func TestSystemRunnerUsesStdinAndExcludesCredentialEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-forward")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-forward")
	t.Setenv("CONCIERGE_SPAWN_GENERATION", "test-generation")
	runner := SystemRunner{Executable: "/bin/sh"}
	stdout, stderr, err := runner.Run(
		context.Background(),
		[]string{
			"-c",
			`cat; printf '\n%s:%s:%s' "${OPENAI_API_KEY-unset}" "${AWS_SECRET_ACCESS_KEY-unset}" "${CONCIERGE_SPAWN_GENERATION-unset}"`,
		},
		strings.NewReader("stdin-only"),
	)
	if err != nil || len(stderr) != 0 || string(stdout) != "stdin-only\nunset:unset:unset" {
		t.Fatal("runner did not keep input on stdin and remove credential environment")
	}
}
