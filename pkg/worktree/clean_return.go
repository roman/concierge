package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanReturn discards checkout files after the caller confirms acceptance,
// landing, lease ownership and the absence of processes using the checkout.
func (client Client) CleanReturn(ctx context.Context, specification Specification) error {
	if err := client.VerifyIdentity(ctx, specification); err != nil {
		return err
	}
	if err := client.checkCheckoutIndex(ctx, specification); err != nil {
		return err
	}
	paths, err := client.returnCleanupPaths(ctx, specification.Path)
	if err != nil {
		return err
	}
	var existingPaths []string
	var before []os.FileInfo
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		existingPaths = append(existingPaths, path)
		before = append(before, info)
	}
	// Report completed removals even when a later command fails. Directory paths
	// cover their descendants without filling the host response with cache files.
	defer func() {
		for index, path := range existingPaths {
			after, err := os.Lstat(path)
			if (os.IsNotExist(err) || (err == nil &&
				(!os.SameFile(before[index], after) || before[index].Mode().Type() != after.Mode().Type()))) &&
				client.RemovedPath != nil {
				client.RemovedPath(path)
			}
		}
	}()
	if _, err := client.git(ctx, specification.Path, "reset", "--hard", "HEAD"); err != nil {
		return err
	}
	if _, err := client.git(ctx, specification.Path, "clean", "-fdx"); err != nil {
		return err
	}
	status, err := client.git(
		ctx,
		specification.Path,
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--ignored=matching",
	)
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("%w: checkout remains dirty after return cleanup", ErrIdentity)
	}
	return nil
}

func (client Client) returnCleanupPaths(ctx context.Context, directory string) ([]string, error) {
	others, err := client.git(
		ctx,
		directory,
		"ls-files",
		"--others",
		"--directory",
		"--killed",
		"-z",
	)
	if err != nil {
		return nil, err
	}
	added, err := client.git(
		ctx,
		directory,
		"diff",
		"--cached",
		"--no-renames",
		"--name-only",
		"--diff-filter=A",
		"-z",
		"HEAD",
	)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range strings.Split(others+added, "\x00") {
		if name != "" {
			paths = append(paths, filepath.Join(directory, name))
		}
	}
	encoded, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	// Refuse before mutation rather than silently dropping removal diagnostics.
	if len(encoded) > 24<<10 {
		return nil, fmt.Errorf(
			"%w: return cleanup path report exceeds 24 KiB; clean the checkout manually",
			ErrIdentity,
		)
	}
	return paths, nil
}
