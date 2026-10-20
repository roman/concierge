package worktree

import (
	"context"
	"strings"
)

// DiscardInventory records source products that require an operator decision.
type DiscardInventory struct {
	Worktree   string
	Branch     string
	BaseCommit string
	Head       string
	Files      string
	Commits    string
}

func (inventory DiscardInventory) RequiresDecision() bool {
	return inventory.Files != "" || inventory.Commits != "" ||
		inventory.Head != inventory.BaseCommit
}

// InspectDiscard never removes ignored files, including generated store links.
// Commits beyond the launch base are conservatively retained even if merged elsewhere.
func (client Client) InspectDiscard(
	ctx context.Context,
	specification Specification,
) (DiscardInventory, error) {
	if err := client.VerifyIdentity(ctx, specification); err != nil {
		return DiscardInventory{}, err
	}
	if err := client.checkCheckoutIndex(ctx, specification); err != nil {
		return DiscardInventory{}, err
	}
	inventory := DiscardInventory{
		Worktree:   specification.Path,
		Branch:     specification.Branch,
		BaseCommit: specification.BaseCommit,
	}
	head, err := client.git(ctx, specification.Path, "rev-parse", "HEAD")
	if err != nil {
		return DiscardInventory{}, err
	}
	inventory.Head = strings.TrimSpace(head)
	inventory.Files, err = client.git(
		ctx,
		specification.Path,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
		"--ignored=matching",
	)
	if err != nil {
		return DiscardInventory{}, err
	}
	inventory.Commits, err = client.git(
		ctx,
		specification.Path,
		"log",
		"--format=%H %s",
		specification.BaseCommit+"..HEAD",
	)
	return inventory, err
}
