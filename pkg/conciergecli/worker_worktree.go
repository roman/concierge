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
	Import        struct{} `cmd:"" help:"Import a selected base from one bounded JSON header line and a Git bundle on stdin."`
	PrepareLaunch struct{} `cmd:"" help:"Prepare a private generation directory and inspect the owned worktree environment from bounded JSON stdin."`
}

func (command *workerWorktreeCommands) run(
	ctx context.Context,
	selected string,
	input io.Reader,
	output io.Writer,
) int {
	var request remoteworker.WorktreeRequest
	var launchRequest remoteworker.LaunchSiteRequest
	var bundle io.Reader
	var err error
	switch selected {
	case "worker-worktree import":
		request, bundle, err = remoteworker.DecodeBaseImport(input)
	case "worker-worktree prepare-launch":
		launchRequest, err = remoteworker.DecodeLaunchSiteRequest(input)
	default:
		request, err = remoteworker.DecodeWorktreeRequest(input)
	}
	if err != nil {
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
	if selected == "worker-worktree prepare-launch" {
		site, err := manager.PrepareLaunchSite(ctx, launchRequest)
		if err != nil {
			return fail(
				output,
				1,
				err,
				"retain the captured lease and generation; inspect remote directory ownership before retrying",
			)
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
	lease, err := manager.Acquire(
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
