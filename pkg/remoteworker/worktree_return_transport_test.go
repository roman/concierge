package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type worktreeReturnSSH struct {
	t             *testing.T
	request       WorktreeReturnRequest
	response      taskstate.WorktreeLease
	calls         int
	rejection     error
	removed       []string
	responseError string
	helperCommit  string
	manager       *Worktrees
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
	if runner.helperCommit != "" && !strings.Contains(
		arguments[len(arguments)-1],
		"/clients/"+runner.helperCommit+"/bin/concierge' 'worker-worktree' 'return' '--protocol' '1'",
	) {
		runner.t.Fatal("did not require the selected compatible return helper")
	}
	request, err := DecodeWorktreeReturnRequest(input)
	if err != nil || !reflect.DeepEqual(request, runner.request) {
		runner.t.Fatal("changed the original lease or return proof")
	}
	if runner.rejection != nil {
		data, _ := json.Marshal(runner.rejection.Error())
		return []byte("error: " + string(data) + "\n"), nil, errors.New("native return refused")
	}
	if runner.manager != nil {
		lease, err := runner.manager.Return(context.Background(), request)
		if err != nil {
			runner.t.Fatalf("new helper could not return old-client journal: %v", err)
		}
		runner.response = lease
	}
	data, err := json.Marshal(
		WorktreeReturnResponse{
			WorktreeLease: runner.response,
			RemovedPaths:  runner.removed,
			Error:         runner.responseError,
		},
	)
	if runner.responseError != "" {
		err = errors.New("remote return failed")
	}
	return data, nil, err
}

func TestPreparedHelperReturnsOriginalClientJournal(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	request.Proof.Outcome = WorktreeScout
	newCommit := strings.Repeat("c", 40)
	if request.Lease.Remote.ClientCommit == newCommit {
		t.Fatal("fixture must retain an older client identity")
	}
	runner := &worktreeReturnSSH{t: t, request: request, helperCommit: newCommit, manager: &manager}
	transport := WorktreeTransport{Runner: runner, ReturnClientCommit: newCommit}
	profile := herdr.SavedMachine{ID: request.Worktree.Location.Machine,
		Target: request.Worktree.Location.Target, Enabled: true}
	expected := request.Lease
	expected.State = taskstate.LeaseReturned
	for range 2 {
		got, err := transport.Return(t.Context(), profile, request)
		if err != nil || got != expected {
			t.Fatalf("old-client lease return: %+v %v", got, err)
		}
	}
	if err := withWorktreeJournal(t.Context(), request.Worktree, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err == nil && (record == nil || record.Request != request.Worktree || record.Lease != expected) {
			t.Fatal("return replaced the journal identity")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReturnReportsRemovedPathsOnPartialFailure(t *testing.T) {
	_, request := remoteReturnFixture(t)
	path := filepath.Join(request.Lease.Path, "generated")
	runner := &worktreeReturnSSH{
		t:             t,
		request:       request,
		removed:       []string{path},
		responseError: "cleanup failed after removal",
	}
	var removed []string
	transport := WorktreeTransport{
		Runner:      runner,
		RemovedPath: func(path string) { removed = append(removed, path) },
	}
	profile := herdr.SavedMachine{
		ID:      request.Worktree.Location.Machine,
		Target:  request.Worktree.Location.Target,
		Enabled: true,
	}
	if _, err := transport.Return(t.Context(), profile, request); err == nil ||
		!strings.Contains(err.Error(), runner.responseError) {
		t.Fatalf("partial failure: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{path}) {
		t.Fatalf("removed paths: %q", removed)
	}
}

func TestNativeReturnTransportRequiresExactReturnedLeaseAndDestination(t *testing.T) {
	_, request := remoteReturnFixture(t)
	request.ReturnIgnoredPaths = []string{"generated/"}
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
	runner.rejection = fmt.Errorf(
		"%w: return requires a clean checkout without ignored files; unmatched ignored paths: %q",
		worktree.ErrIdentity,
		[]string{"unknown"},
	)
	if _, err := transport.Return(t.Context(), profile, request); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("lost unmatched ignored path: %v", err)
	}
	runner.rejection = nil
	before := runner.calls
	profile.Target = "changed-target"
	if _, err := transport.Return(context.Background(), profile, request); err == nil ||
		runner.calls != before {
		t.Fatal("contacted a changed SSH target")
	}
}
