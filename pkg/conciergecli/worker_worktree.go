package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/worktree"
)

type workerWorktreeCommands struct {
	Acquire struct{} `cmd:"" help:"Acquire or recover an owned remote lease from a bounded JSON request on stdin."`
}

func (command *workerWorktreeCommands) run(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
) int {
	request, err := remoteworker.DecodeWorktreeRequest(input)
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
	lease, err := (remoteworker.Worktrees{Git: worktree.Client{Executable: git}, Treehouse: pool}).Acquire(
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
