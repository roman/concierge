package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

type worktreeReturnSSH struct {
	t         *testing.T
	request   WorktreeReturnRequest
	response  taskstate.WorktreeLease
	calls     int
	rejection error
}

func (runner *worktreeReturnSSH) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	runner.calls++
	if !strings.Contains(arguments[len(arguments)-1], "'worker-worktree' 'return'") {
		runner.t.Fatal("used an unfixed return command")
	}
	request, err := DecodeWorktreeReturnRequest(input)
	if err != nil || request != runner.request {
		runner.t.Fatal("changed the original lease or return proof")
	}
	if runner.rejection != nil {
		data, _ := json.Marshal(runner.rejection.Error())
		return []byte("error: " + string(data) + "\n"), nil, errors.New("native return refused")
	}
	data, err := json.Marshal(runner.response)
	return data, nil, err
}

func TestNativeReturnTransportRequiresExactReturnedLeaseAndDestination(t *testing.T) {
	_, request := remoteReturnFixture(t)
	expected := request.Lease
	expected.State = taskstate.LeaseReturned
	runner := &worktreeReturnSSH{t: t, request: request, response: expected}
	transport := WorktreeTransport{Runner: runner}
	profile := herdr.SavedMachine{
		ID:      request.Worktree.Location.Machine,
		Target:  request.Worktree.Location.Target,
		Enabled: true,
	}
	got, err := transport.Return(context.Background(), profile, request)
	if err != nil || got != expected {
		t.Fatalf("confirmed native return: %+v %v", got, err)
	}
	for _, mismatch := range []string{"path", "state", "holder", "machine"} {
		runner.response = expected
		switch mismatch {
		case "path":
			runner.response.Path = "/foreign/worktree"
		case "state":
			runner.response.State = taskstate.LeaseHeld
		case "holder":
			runner.response.Holder = "foreign-holder"
		case "machine":
			runner.response.Remote.Machine = "foreign-machine"
		}
		if _, err := transport.Return(context.Background(), profile, request); err == nil {
			t.Fatalf("accepted returned %s mismatch", mismatch)
		}
	}
	for _, rejection := range []error{ErrWorktreeBusy, ErrWorktreeQuietUnconfirmed} {
		runner.rejection = rejection
		if _, err := transport.Return(context.Background(), profile, request); !errors.Is(
			err,
			rejection,
		) {
			t.Fatalf("lost the operator's cleanup step: %v", err)
		}
	}
	runner.rejection = nil
	before := runner.calls
	profile.Target = "changed-target"
	if _, err := transport.Return(context.Background(), profile, request); err == nil ||
		runner.calls != before {
		t.Fatal("contacted a changed SSH target")
	}
}
