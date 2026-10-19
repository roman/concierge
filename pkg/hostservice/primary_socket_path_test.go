package hostservice

import (
	"strings"
	"testing"
)

func TestRemoteLaunchRejectsLongPrimarySocketBeforeEffects(t *testing.T) {
	server, input, runner := remoteHostLaunchFixture(t)
	server.config.Root = "/" + strings.Repeat("p", 90)
	input.AgentArgs = []string{"--sandbox=workspace-write"}
	_, err := server.executeInitialLaunch(t.Context(), input, operatorLaunch)
	if err == nil || !strings.Contains(err.Error(), "primary worker socket") ||
		!strings.Contains(err.Error(), "bytes") || !strings.Contains(err.Error(), "--root") {
		t.Fatalf("missing early primary path guidance: %v", err)
	}
	task, readErr := server.store.Read(t.Context(), input.TaskID)
	if readErr != nil || task.Snapshot().LaunchIntent != nil || task.Snapshot().Worker != nil ||
		runner.clientReads != 0 || len(runner.imports) != 0 {
		t.Fatalf("overlong primary socket caused effects: %+v %v", task, readErr)
	}
}
