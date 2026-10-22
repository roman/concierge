package hostservice

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/versioncheck"
	"github.com/roman/tincan/pkg/inbox"
)

const launchVersionStatus = `client:
  version: 0.9.5
server:
  status: running
  version: 0.9.0
  endpoint_compatible: yes
  private_protocol_compatible: yes
update:
  server_binary_stale: yes
`

type versionLaunchSSH struct {
	*interruptedLaunchSSH
	status string
}

func (runner versionLaunchSSH) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	if arguments[0] == "-G" {
		return []byte(
			"hostname dev-remote\nforwardagent no\nforwardx11 no\ntunnel false\n",
		), nil, nil
	}
	command := arguments[len(arguments)-1]
	if strings.Contains(command, "'herdr' 'status'") {
		return []byte(runner.status), nil, nil
	}
	if strings.Contains(command, "'codex' '--version'") {
		return []byte("codex-cli 2.0"), nil, nil
	}
	return runner.interruptedLaunchSSH.Run(ctx, arguments, input)
}

func TestRemoteVersionPreflightRunsBeforeResources(t *testing.T) {
	for _, incompatible := range []string{"yes", "no"} {
		t.Run(incompatible, func(t *testing.T) {
			server, input, runner := remoteHostLaunchFixture(t)
			server.config.Revision = strings.Repeat("b", 40)
			server.versionProbe = func(_ context.Context, executable string, args ...string) ([]byte, error) {
				if args[0] == "status" {
					return []byte(
						strings.ReplaceAll(
							strings.ReplaceAll(launchVersionStatus, "0.9.0", "0.9.5"),
							"stale: yes",
							"stale: no",
						),
					), nil
				}
				return []byte("codex-cli 1.0"), nil
			}
			status := strings.Replace(
				launchVersionStatus,
				"private_protocol_compatible: yes",
				"private_protocol_compatible: "+incompatible,
				1,
			)
			server.preparationSSH = versionLaunchSSH{runner, status}
			response, err := server.executeInitialLaunch(t.Context(), input, operatorLaunch)
			if err == nil || response.Versions == nil || len(response.Versions.Warnings) == 0 {
				t.Fatalf("missing warnings: %+v %v", response, err)
			}
			task, _ := server.store.Read(t.Context(), input.TaskID)
			if incompatible == "no" {
				if !response.Versions.Blocked() || task.Snapshot().LaunchIntent != nil ||
					len(runner.imports) != 0 {
					t.Fatalf(
						"incompatible protocol created resources: %+v %v",
						task.Snapshot(),
						err,
					)
				}
			} else if response.Versions.Blocked() || task.Snapshot().LaunchIntent == nil || len(runner.imports) != 1 {
				t.Fatalf("plain differences blocked import: %+v %v", task.Snapshot(), err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			pending, readErr := messages.List(server.config.Recipient)
			if readErr != nil || len(pending) != 1 ||
				!strings.Contains(pending[0].Body, "task-version-warning") {
				t.Fatalf("missing durable warning: %+v %v", pending, readErr)
			}
		})
	}
}

func TestLocalPreflightWarnsAboutStaleServer(t *testing.T) {
	server := workerHost(t, 3)
	server.versionProbe = func(context.Context, string, ...string) ([]byte, error) { return []byte(launchVersionStatus), nil }
	report, err := server.checkLaunchVersions(
		t.Context(),
		LaunchRequest{TaskID: "task-1", Harness: "claude"},
		versioncheck.Options{},
	)
	if err != nil || report.Blocked() {
		t.Fatalf("stale server blocked: %+v %v", report, err)
	}
	for _, warning := range report.Warnings {
		if warning.Component == "server_binary_stale" {
			return
		}
	}
	t.Fatal("missing local stale warning")
}
