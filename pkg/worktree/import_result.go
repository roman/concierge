package worktree

import (
	"context"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImportResult retains an immutable result in the primary's shared Git database.
// It changes neither a checked-out branch nor the repository-wide FETCH_HEAD.
func (client Client) ImportResult(
	ctx context.Context,
	project, commit, reference string,
	input io.Reader,
) error {
	decoded, err := hex.DecodeString(commit)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) ||
		strings.ToLower(commit) != commit {
		return ErrIdentity
	}
	if input == nil || !filepath.IsAbs(client.Executable) || !filepath.IsAbs(project) ||
		!strings.HasPrefix(reference, "refs/concierge/results/") {
		return ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := client.git(ctx, project, "check-ref-format", reference); err != nil {
		return err
	}
	expected := reference + " " + commit
	current, err := client.resultReference(ctx, project, reference)
	if err != nil || (current != "" && current != expected) {
		return ErrIdentity
	}
	directory, err := os.MkdirTemp("", "concierge-result-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "result.bundle")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, input)
	if err := file.Close(); copyErr != nil || err != nil {
		if copyErr != nil {
			return copyErr
		}
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	heads, err := client.git(ctx, project, "bundle", "list-heads", path)
	if err != nil || strings.TrimSpace(heads) != commit+" HEAD" {
		return ErrIdentity
	}
	if _, err := client.git(ctx, project, "bundle", "verify", path); err != nil {
		return err
	}
	if _, err := client.git(ctx, project, "fetch", "--no-tags", "--no-write-fetch-head", "--", path, "HEAD"); err != nil {
		return err
	}
	kind, err := client.git(ctx, project, "cat-file", "-t", commit)
	if err != nil || strings.TrimSpace(kind) != "commit" {
		return ErrIdentity
	}
	if current == "" {
		if _, err := client.git(ctx, project, "update-ref", reference, commit, strings.Repeat("0", len(commit))); err != nil {
			// Another identical import can publish the same immutable reference.
			confirmed, checkErr := client.resultReference(ctx, project, reference)
			if checkErr != nil || confirmed != expected {
				return err
			}
		}
	}
	confirmed, err := client.resultReference(ctx, project, reference)
	if err != nil || confirmed != expected {
		return ErrIdentity
	}
	return nil
}

func (client Client) resultReference(
	ctx context.Context,
	project, reference string,
) (string, error) {
	output, err := client.git(
		ctx,
		project,
		"for-each-ref",
		"--format=%(refname) %(objectname)",
		reference,
	)
	return strings.TrimSpace(output), err
}
