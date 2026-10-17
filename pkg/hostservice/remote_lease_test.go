package hostservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

type retirementLeaseSSH struct {
	t           *testing.T
	request     remoteworker.WorktreeRequest
	lease       taskstate.WorktreeLease
	outcome     remoteworker.WorktreeReturnOutcome
	inspections int
	returns     int
}

func (runner *retirementLeaseSSH) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	command := arguments[len(arguments)-1]
	lease := runner.lease
	switch {
	case strings.Contains(command, "'worker-worktree' 'inspect'"):
		runner.inspections++
		request, err := remoteworker.DecodeWorktreeRequest(input)
		if err != nil || request != runner.request {
			runner.t.Fatal("changed the abandoned native acquisition")
		}
	case strings.Contains(command, "'worker-worktree' 'return'"):
		runner.returns++
		request, err := remoteworker.DecodeWorktreeReturnRequest(input)
		if err != nil || request.Worktree != runner.request || request.Lease != lease ||
			request.Proof.Outcome != runner.outcome ||
			request.Proof.Commit != runner.request.BaseCommit {
			runner.t.Fatal("changed the original native return scope or proof")
		}
		lease.State = taskstate.LeaseReturned
	default:
		return nil, nil, errors.New("unexpected native retirement operation")
	}
	data, err := json.Marshal(lease)
	return data, nil, err
}

func TestHostReturnsOnlyItsRecordedNativeLease(t *testing.T) {
	for _, mode := range []string{"confirmed", "changed target", "foreign primary", "different repository"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRemoteActivationFixture(t)
			server := fixture.server
			profile := fixture.profile
			if mode == "changed target" {
				profile.Target = "changed-target"
			}
			server.preparationMachines = launchLocationCatalog{profile}
			runner := &retirementLeaseSSH{
				t:       t,
				request: fixture.request.Worktree,
				lease:   fixture.request.Lease,
				outcome: remoteworker.WorktreeUnchanged,
			}
			server.preparationSSH = runner
			ctx := context.Background()
			if _, err := server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
				task, err := task.CompleteInitialLaunch("gen-1")
				if err == nil {
					task, err = task.AcceptResult("gen-1", taskstate.Close, "2026-10-17")
				}
				if err == nil {
					task, err = task.RetireResult("gen-1")
				}
				if err != nil {
					return taskstate.Task{}, err
				}
				snapshot := task.Snapshot()
				if mode == "foreign primary" {
					snapshot.Receipt.Lease.Remote.PrimaryRoot = "/foreign-primary"
				}
				if mode == "different repository" {
					snapshot.Definition.Repo = "different-repository"
				}
				return taskstate.NewMigrated(snapshot)
			}); err != nil {
				t.Fatal(err)
			}
			result, err := server.service.ReleaseWorktree(ctx, "task-1", "gen-1")
			if mode != "confirmed" {
				if err == nil || runner.returns != 0 || runner.inspections != 0 {
					t.Fatalf("changed scope reached native cleanup: %v", err)
				}
				return
			}
			if err != nil || result.Snapshot().Receipt.Lease.State != taskstate.LeaseReturned ||
				runner.returns != 1 ||
				runner.inspections != 0 {
				t.Fatalf("native lease return: %+v %v", result.Snapshot(), err)
			}
			if _, err := server.service.ReleaseWorktree(ctx, "task-1", "gen-1"); err != nil ||
				runner.returns != 1 {
				t.Fatalf("repeated a confirmed native return: %v", err)
			}
		})
	}
}

func TestHostRecoversAnAbandonedNativeLeaseWithoutAcquiringAgain(t *testing.T) {
	fixture := newRemoteActivationFixture(t)
	server := fixture.server
	server.preparationMachines = launchLocationCatalog{fixture.profile}
	runner := &retirementLeaseSSH{
		t:       t,
		request: fixture.request.Worktree,
		lease:   fixture.request.Lease,
		outcome: remoteworker.WorktreeAbandoned,
	}
	server.preparationSSH = runner
	ctx := context.Background()
	if _, err := server.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		snapshot.Worker, snapshot.State = nil, taskstate.Queued
		intent := snapshot.LaunchIntent
		intent.Lease, intent.Worktree = taskstate.WorktreeLease{}, ""
		intent.Endpoint, intent.ResumeArgs, intent.Phase = nil, nil, taskstate.LaunchLeaseRequested
		abandoned, err := taskstate.NewMigrated(snapshot)
		if err != nil {
			return taskstate.Task{}, err
		}
		return abandoned.AbandonInitialLaunch("gen-1")
	}); err != nil {
		t.Fatal(err)
	}
	result, err := server.service.ReleaseWorktree(ctx, "task-1", "gen-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, found := result.RetiredLease("gen-1")
	if !found || lease.State != taskstate.LeaseReturned || runner.inspections != 1 ||
		runner.returns != 1 {
		t.Fatal("did not recover and return the original native lease")
	}
}
