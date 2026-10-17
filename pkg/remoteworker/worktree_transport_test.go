package remoteworker

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

type worktreeSSHFixture struct {
	t         *testing.T
	request   WorktreeRequest
	response  taskstate.WorktreeLease
	calls     int
	operation string
}

func (fixture *worktreeSSHFixture) Run(
	_ context.Context,
	arguments []string,
	stdin io.Reader,
) ([]byte, []byte, error) {
	fixture.calls++
	command := arguments[len(arguments)-1]
	operation := fixture.operation
	if operation == "" {
		operation = "acquire"
	}
	if !strings.Contains(command, "'worker-worktree' '"+operation+"'") ||
		!strings.Contains(command, "'"+fixture.request.Location.ClientPath()+"'") {
		fixture.t.Fatalf("unexpected native command: %s", command)
	}
	var received WorktreeRequest
	if err := json.NewDecoder(stdin).Decode(&received); err != nil || received != fixture.request {
		fixture.t.Fatal("changed the worktree request on SSH stdin")
	}
	data, err := json.Marshal(fixture.response)
	return data, nil, err
}

func TestRemoteWorktreeTransportPinsMachineAndAcquisitionIdentity(t *testing.T) {
	request := remoteWorktreeRequest(t)
	location := request.Location
	location.Backend = taskstate.RemoteGit
	lease := taskstate.WorktreeLease{
		Remote:     location,
		ID:         "lease-one",
		Holder:     request.Holder,
		Project:    location.ProjectPath(),
		Path:       filepath.Join(location.PoolRoot(), "slot", "repo"),
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
		State:      taskstate.LeaseHeld,
	}
	profile := herdr.SavedMachine{ID: location.Machine, Target: location.Target, Enabled: true}
	fixture := &worktreeSSHFixture{t: t, request: request, response: lease}
	transport := WorktreeTransport{Runner: fixture}
	got, err := transport.Acquire(context.Background(), profile, request)
	if err != nil || got != lease {
		t.Fatalf("confirmed acquisition: %+v %v", got, err)
	}
	fixture.response.Branch = "task/foreign"
	if _, err := transport.Acquire(context.Background(), profile, request); err == nil {
		t.Fatal("accepted a lease for another branch")
	}
	profile.Target = "changed-target"
	if _, err := transport.Acquire(context.Background(), profile, request); err == nil {
		t.Fatal("followed a changed saved target")
	}
	if fixture.calls != 2 {
		t.Fatal("contacted a changed target")
	}
}
