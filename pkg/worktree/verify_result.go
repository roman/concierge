package worktree

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// VerifyCheckoutCommit requires the recorded branch and exact selected commit.
func (client Client) VerifyCheckoutCommit(ctx context.Context, specification Specification) error {
	if err := client.VerifyIdentity(ctx, specification); err != nil {
		return err
	}
	head, err := client.git(ctx, specification.Path, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != specification.BaseCommit {
		return ErrIdentity
	}
	return nil
}

// VerifyResult confirms a completed import without contacting the remote worker.
func (client Client) VerifyResult(ctx context.Context, project, commit, reference string) error {
	decoded, err := hex.DecodeString(commit)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) ||
		strings.ToLower(commit) != commit {
		return ErrIdentity
	}
	if !filepath.IsAbs(client.Executable) || !filepath.IsAbs(project) ||
		!strings.HasPrefix(reference, "refs/concierge/results/") {
		return ErrIdentity
	}
	if _, err := client.git(ctx, project, "check-ref-format", reference); err != nil {
		return err
	}
	confirmed, err := client.resultReference(ctx, project, reference)
	if err != nil || confirmed != reference+" "+commit {
		return ErrIdentity
	}
	kind, err := client.git(ctx, project, "cat-file", "-t", commit)
	if err != nil || strings.TrimSpace(kind) != "commit" {
		return ErrIdentity
	}
	return nil
}
