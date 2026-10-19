package taskops

import (
	"strings"
	"testing"
)

func TestLaunchProtocolsCarryWorkerReplyContract(t *testing.T) {
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
			for _, heading := range []string{
				"## Handoff", "### Outcome", "### Current state", "### Next actions",
				"## Gaps", "#### Gap", "#### Why it matters",
				"## Learnings", "#### Description", "#### Type", "#### Body",
				"## Artifacts", "#### Reusable for",
			} {
				if !strings.Contains(test.protocol, "`"+heading+"`") {
					t.Errorf("missing exact heading %q", heading)
				}
			}
			if !strings.Contains(test.protocol, "Never write into a vault yourself.") {
				t.Error("missing vault write prohibition")
			}
			if strings.Contains(test.protocol, "locker ") {
				t.Error("worker instructions include a Locker command")
			}
		})
	}
}
