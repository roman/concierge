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
	Acquire struct{} `cmd:"" help:"Acquire or recover an owned remote lease from a bounded JSON request on stdin."`
	Import  struct{} `cmd:"" help:"Import a selected base from one bounded JSON header line and a Git bundle on stdin."`
}

func (command *workerWorktreeCommands) run(
	ctx context.Context,
	selected string,
	input io.Reader,
	output io.Writer,
) int {
	var request remoteworker.WorktreeRequest
	var bundle io.Reader
	var err error
	if selected == "worker-worktree import" {
		request, bundle, err = remoteworker.DecodeBaseImport(input)
	} else {
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
