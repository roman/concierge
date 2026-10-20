package taskops

import (
	"strings"
	"testing"
)

func TestWorkerProtocolsKeepReportPolicyExternal(t *testing.T) {
	service, _, messageRoot := runningWorkerService(t)
	local, err := service.workerProtocol("task-1", messageRoot)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := RemoteWorkerProtocol("task-1", RemoteWorkerTools{
		Concierge: "/remote/concierge", TaskDirectory: "/remote/task-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, protocol string }{
		{"local", local}, {"remote", remote},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocol := strings.ToLower(test.protocol)
			for _, policy := range []string{
				"vault", "locker", "## handoff", "## gaps", "## learnings", "## artifacts",
			} {
				if strings.Contains(protocol, policy) {
					t.Errorf("worker protocol includes external report policy %q", policy)
				}
			}
		})
	}
}
