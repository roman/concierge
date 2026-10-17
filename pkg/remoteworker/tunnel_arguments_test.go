package remoteworker

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTunnelArgumentsForwardOnlyWorkerSocket(t *testing.T) {
	root, spec := tunnelFixture(t)
	localSocket := filepath.Join(root, "worker.sock")
	arguments, err := TunnelArguments(spec, localSocket)
	if err != nil {
		t.Fatal(err)
	}
	forward := "/remote/task/worker.sock:" + localSocket
	if !slices.Contains(arguments, forward) || !slices.Contains(arguments, "-R") ||
		!slices.Contains(arguments, "-N") || arguments[len(arguments)-1] != spec.Target {
		t.Fatalf("wrong tunnel command: %q", arguments)
	}
	for _, required := range []string{"BatchMode=yes", "ForwardAgent=no", "ClearAllForwardings=no",
		"SetEnv=CONCIERGE_SSH_TRANSPORT=1",
		"StrictHostKeyChecking=yes", "UpdateHostKeys=no", "ExitOnForwardFailure=yes",
		"ControlMaster=no", "ForkAfterAuthentication=no", "StreamLocalBindUnlink=no"} {
		if !slices.Contains(arguments, required) {
			t.Fatalf("missing SSH option %s", required)
		}
	}
	inspection, err := TunnelConfigArguments(spec, localSocket)
	if err != nil || !slices.Contains(inspection, "-G") || slices.Contains(inspection, "-R") {
		t.Fatalf(
			"preflight must inspect configured forwards before adding its own: %q %v",
			inspection,
			err,
		)
	}
}

func TestTunnelArgumentsRejectHostSocketAndAmbiguousPaths(t *testing.T) {
	root, spec := tunnelFixture(t)
	for _, path := range []string{filepath.Join(root, "host.sock"), "worker.sock", "/tmp/a:b/worker.sock"} {
		if _, err := TunnelArguments(spec, path); err == nil {
			t.Fatalf("unsafe socket path accepted: %s", path)
		}
	}
	spec.Tools.TaskDirectory = "/remote/a:b"
	if _, err := TunnelArguments(spec, filepath.Join(root, "worker.sock")); err == nil {
		t.Fatal("ambiguous remote socket path accepted")
	}
}

func TestTunnelConfigurationRefusesExistingForwarding(t *testing.T) {
	if err := ValidateTunnelConfiguration([]byte("host machine\nhostname machine\nclearallforwardings no\n")); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{
		"localforward 8080 localhost:80", "remoteforward /remote.sock /local.sock",
		"dynamicforward 1080", "tunnel true", "hostname duplicate", "invalid",
	} {
		err := ValidateTunnelConfiguration([]byte("hostname machine\n" + extra + "\n"))
		if err == nil || !strings.Contains(err.Error(), "forwarding-free") {
			t.Fatalf("unsafe SSH configuration accepted: %s error=%v", extra, err)
		}
	}
}
