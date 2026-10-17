package remoteworker

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type launchSiteSSHFixture struct {
	t        *testing.T
	request  LaunchSiteRequest
	response LaunchSite
	calls    int
}

func (fixture *launchSiteSSHFixture) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	fixture.calls++
	if !strings.Contains(arguments[len(arguments)-1], "'worker-worktree' 'prepare-launch'") {
		fixture.t.Fatal("used an unfixed setup command")
	}
	request, err := DecodeLaunchSiteRequest(input)
	if err != nil || request != fixture.request {
		fixture.t.Fatal("changed the prepared launch identity")
	}
	data, err := json.Marshal(fixture.response)
	return data, nil, err
}

func TestRemoteLaunchSiteTransportPinsLeaseAndPrivatePaths(t *testing.T) {
	manager, request := launchSiteFixture(t)
	site, err := manager.PrepareLaunchSite(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &launchSiteSSHFixture{t: t, request: request, response: site}
	profile := herdr.SavedMachine{
		ID:      request.Worktree.Location.Machine,
		Target:  request.Worktree.Location.Target,
		Enabled: true,
	}
	transport := WorktreeTransport{Runner: fixture}
	got, err := transport.PrepareLaunchSite(context.Background(), profile, request)
	if err != nil || got != site {
		t.Fatalf("confirmed site: %+v %v", got, err)
	}
	fixture.response.Tools.TaskDirectory = "/foreign/task"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("accepted caller-selected paths")
	}
	fixture.response = site
	fixture.response.Environment = "unknown"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("accepted an unknown environment")
	}
	profile.Target = "changed-target"
	if _, err := transport.PrepareLaunchSite(context.Background(), profile, request); err == nil {
		t.Fatal("followed a changed target")
	}
	if fixture.calls != 3 {
		t.Fatal("contacted the changed target")
	}
}
