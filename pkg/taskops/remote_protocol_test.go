package taskops

import (
	"strings"
	"testing"
)

func TestRemoteProtocolUsesOnlyRemoteWorkerCommands(t *testing.T) {
	tools := RemoteWorkerTools{
		Concierge:     "/remote/tools/concierge",
		TaskDirectory: "/remote/tasks/task one",
	}
	protocol, err := RemoteWorkerProtocol("task-1", tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Keep existing user instructions and native permissions active",
		"standalone shell check", "Do not access the mailbox during setup",
		"Never set or export a generation copied",
		"request permission through the normal native approval workflow",
		"Stop and report a rejected native approval",
		"Stop retries after access is revoked",
		"'/remote/tools/concierge' task inbox list 'task-1'",
		"task receive 'task-1' MESSAGE_ID --generation \"$CONCIERGE_SPAWN_GENERATION\"",
		"task settings 'task-1' --generation \"$CONCIERGE_SPAWN_GENERATION\"",
		"task bootstrap read 'task-1' MESSAGE_ID",
		"task bootstrap ack 'task-1' MESSAGE_ID",
		"task reply send 'task-1'", "--assignment-id MESSAGE_ID",
		"'/remote/tasks/task one/result.md'", "task ack 'task-1' MESSAGE_ID",
		"primary fetches the branch", "do not push or open a PR",
		"Scouts follow their read-only contract",
	} {
		if !strings.Contains(protocol, required) {
			t.Errorf("remote protocol lacks %q", required)
		}
	}
	for _, forbidden := range []string{" --root ", " --message-root ", " --recipient ", "tincan", "backlog show", "spawn_generation:"} {
		if strings.Contains(protocol, forbidden) {
			t.Errorf("remote protocol exposes a local-file command: %q", forbidden)
		}
	}
	if strings.ContainsAny(protocol, "\r\n") {
		t.Fatal("remote protocol contains transport-unsafe line breaks")
	}
}

func TestRemoteProtocolRejectsUnsafeTaskAndToolPaths(t *testing.T) {
	for _, trial := range []struct{ task, executable, directory string }{
		{"../task", "/remote/concierge", "/remote/task"},
		{"task-1", "concierge", "/remote/task"},
		{"task-1", "/remote/concierge\nwrong", "/remote/task"},
		{"task-1", "/remote/concierge", "/"},
		{"task-1", "/remote/concierge", "/remote/../other"},
	} {
		if _, err := RemoteWorkerProtocol(trial.task, RemoteWorkerTools{Concierge: trial.executable, TaskDirectory: trial.directory}); err == nil {
			t.Fatalf("unsafe remote launch context accepted: %+v", trial)
		}
	}
}
