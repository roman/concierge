package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/worktree"
)

type workerWorktreeCommands struct {
	Acquire       struct{} `cmd:"" help:"Acquire or recover an owned remote lease from a bounded JSON request on stdin."`
	Inspect       struct{} `cmd:"" help:"Inspect a completed native acquisition without acquiring or preparing another worktree."`
	ExportResult  struct{} `cmd:"" help:"Export an owned clean result as one JSON header line and an immutable Git bundle."`
	Import        struct{} `cmd:"" help:"Import a selected base from one bounded JSON header line and a Git bundle on stdin."`
	PrepareLaunch struct{} `cmd:"" help:"Prepare a private generation directory and inspect the owned worktree environment from bounded JSON stdin."`
	Return        struct {
		Protocol string `default:"1" enum:"1" help:"Require return protocol 1, including accepted scouts and original-client journals."`
	} `cmd:"" help:"Return a clean, quiet remote lease with the primary's immutable commit proof from bounded JSON stdin."`
}

func (command *workerWorktreeCommands) run(
	ctx context.Context,
	selected string,
	input io.Reader,
	output io.Writer,
) int {
	var request remoteworker.WorktreeRequest
	var launchRequest remoteworker.LaunchSiteRequest
	var returnRequest remoteworker.WorktreeReturnRequest
	var resultRequest remoteworker.WorktreeResultRequest
	var bundle io.Reader
	var err error
	switch selected {
	case "worker-worktree import":
		request, bundle, err = remoteworker.DecodeBaseImport(input)
	case "worker-worktree prepare-launch":
		launchRequest, err = remoteworker.DecodeLaunchSiteRequest(input)
	case "worker-worktree return":
		returnRequest, err = remoteworker.DecodeWorktreeReturnRequest(input)
	case "worker-worktree export-result":
		resultRequest, err = remoteworker.DecodeWorktreeResultRequest(input)
	default:
		request, err = remoteworker.DecodeWorktreeRequest(input)
	}
	if err != nil {
		if selected == "worker-worktree return" {
			response := remoteworker.WorktreeReturnResponse{}
			response.SetFailure(err)
			_ = json.NewEncoder(output).Encode(response)
			return 2
		}
		return fail(
			output,
			2,
			err,
			"provide the original bounded worktree request on stdin from the primary",
		)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return fail(output, 1, err, "make Git available to the remote Concierge client")
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return fail(output, 1, err, "use an absolute Git executable")
	}
	pool, err := exec.LookPath("treehouse")
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		return fail(
			output,
			1,
			err,
			"repair the remote Treehouse executable; retain the existing lease",
		)
	}
	if err == nil {
		pool, err = filepath.Abs(pool)
		if err != nil {
			return fail(output, 1, err, "use an absolute Treehouse executable")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	manager := remoteworker.Worktrees{Git: worktree.Client{Executable: git}, Treehouse: pool}
	if selected == "worker-worktree export-result" {
		bundle, err := manager.ExportResult(ctx, resultRequest)
		if err != nil {
			return fail(
				output,
				1,
				err,
				"retain the lease and selected commit; commit all work and retry from the primary",
			)
		}
		defer bundle.Close()
		result := remoteworker.WorktreeResult{Lease: resultRequest.Lease, Commit: bundle.Commit()}
		if json.NewEncoder(output).Encode(result) != nil {
			return 1
		}
		if _, err := io.Copy(output, bundle); err != nil {
			return 1
		}
		return 0
	}
	if selected == "worker-worktree return" {
		response := remoteworker.WorktreeReturnResponse{}
		manager.Git.RemovedPath = func(path string) { response.RemovedPaths = append(response.RemovedPaths, path) }
		lease, err := manager.Return(ctx, returnRequest)
		response.WorktreeLease = lease
		if err != nil {
			response.SetFailure(err)
		}
		if json.NewEncoder(output).Encode(response) != nil || err != nil {
			return 1
		}
		return 0
	}
	if selected == "worker-worktree prepare-launch" {
		site, err := manager.PrepareLaunchSite(ctx, launchRequest)
		if err != nil {
			_ = remoteworker.EncodeLaunchSiteFailure(output, err)
			return 1
		}
		if json.NewEncoder(output).Encode(site) != nil {
			return 1
		}
		return 0
	}
	if selected == "worker-worktree import" {
		if err := manager.ImportBase(ctx, request, bundle); err != nil {
			return fail(
				output,
				1,
				err,
				"retain the original base and lease holder; retry the same import from the primary",
			)
		}
		if _, err := fmt.Fprintln(output, "status: imported"); err != nil {
			return 1
		}
		return 0
	}
	leaseOperation := manager.Acquire
	if selected == "worker-worktree inspect" {
		leaseOperation = manager.Inspect
	}
	lease, err := leaseOperation(
		ctx,
		request,
	)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"retain the lease journal and retry the identical request from the primary",
		)
	}
	if json.NewEncoder(output).Encode(lease) != nil {
		return 1
	}
	return 0
}
