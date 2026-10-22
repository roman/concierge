package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// VerifyReturn protects a result checkout before the pool parks it on another commit.
func (client Client) VerifyReturn(ctx context.Context, specification Specification) error {
	if err := client.VerifyIdentity(ctx, specification); err != nil {
		return err
	}
	return client.verifyReturnContents(ctx, specification)
}

func (client Client) verifyReturnContents(ctx context.Context, specification Specification) error {
	if err := client.checkCheckoutIndex(ctx, specification); err != nil {
		return err
	}
	if err := client.removeIgnoredStoreLinks(ctx, specification.Path); err != nil {
		return err
	}
	if err := client.removeEmptyIgnoredDirectories(ctx, specification.Path); err != nil {
		return err
	}
	// A reset can remove .gitignore before cleaning files that were ignored.
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
		return fmt.Errorf("%w: return requires a clean checkout without ignored files", ErrIdentity)
	}
	return nil
}

func (client Client) removeEmptyIgnoredDirectories(ctx context.Context, directory string) error {
	entries, err := client.git(
		ctx,
		directory,
		"ls-files",
		"--others",
		"--ignored",
		"--exclude-standard",
		"--directory",
		"-z",
	)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range strings.Split(entries, "\x00") {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		var directories []string
		err := fs.WalkDir(
			root.FS(),
			strings.TrimSuffix(name, "/"),
			func(path string, entry fs.DirEntry, err error) error {
				if errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				if err != nil {
					return err
				}
				if entry.IsDir() {
					directories = append(directories, path)
				}
				return nil
			},
		)
		if err != nil {
			return err
		}
		for index := len(directories) - 1; index >= 0; index-- {
			// Remove only empty directories. Files and symlink targets remain protected.
			if err := root.Remove(directories[index]); err != nil &&
				!errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) &&
				!errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (client Client) removeIgnoredStoreLinks(ctx context.Context, directory string) error {
	entries, err := client.git(
		ctx,
		directory,
		"ls-files",
		"--others",
		"--ignored",
		"--exclude-standard",
		"-z",
	)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range strings.Split(entries, "\x00") {
		if name == "" {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := root.Readlink(name)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(filepath.Clean(target), "/nix/store/") {
			continue
		}
		if err := root.Remove(name); err != nil {
			return err
		}
		if client.RemovedStoreLink != nil {
			client.RemovedStoreLink(filepath.Join(directory, name))
		}
	}
	return nil
}

// VerifyUnusedLeaseReturn also permits a detached acquisition with a reachable commit.
func (client Client) VerifyUnusedLeaseReturn(
	ctx context.Context,
	specification Specification,
) error {
	if err := client.Validate(ctx, specification); err != nil {
		return err
	}
	source, err := client.git(ctx, specification.Project, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if samePath(strings.TrimSuffix(source, "\n"), specification.Path) {
		return ErrIdentity
	}
	registered, err := client.registration(ctx, specification)
	if err != nil {
		return err
	}
	if registered == nil || registered.locked || registered.stale ||
		(registered.branch != "" && registered.branch != "refs/heads/"+specification.Branch) {
		return ErrIdentity
	}
	if err := client.verifyRepository(ctx, specification); err != nil {
		return err
	}
	if registered.branch == "" {
		references, err := client.git(
			ctx,
			specification.Path,
			"for-each-ref",
			"--contains=HEAD",
			"--format=%(refname)",
			"refs/heads",
			"refs/remotes",
			"refs/tags",
		)
		if err != nil {
			return err
		}
		if strings.TrimSpace(references) == "" {
			return fmt.Errorf(
				"%w: detached work is not reachable from a durable reference",
				ErrIdentity,
			)
		}
	} else if err := client.VerifyIdentity(ctx, specification); err != nil {
		return err
	}
	return client.verifyReturnContents(ctx, specification)
}
