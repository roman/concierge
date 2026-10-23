package worktree

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
)

// ValidateReturnIgnoredPaths accepts literal files and directory prefixes ending in a slash.
func ValidateReturnIgnoredPaths(paths []string) error {
	for _, entry := range paths {
		name := strings.TrimSuffix(entry, "/")
		if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name ||
			strings.ContainsAny(entry, "\\*?[]\x00\r\n") {
			return fmt.Errorf(
				"invalid return ignored path %q: use a repo-relative file or directory/",
				entry,
			)
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".." || strings.EqualFold(component, ".git") {
				return fmt.Errorf(
					"invalid return ignored path %q: .. and .git are protected",
					entry,
				)
			}
		}
	}
	return nil
}

func matchesReturnIgnoredPath(name string, paths []string) bool {
	for _, entry := range paths {
		if strings.HasSuffix(entry, "/") {
			if strings.HasPrefix(name, entry) {
				return true
			}
		} else if name == entry {
			return true
		}
	}
	return false
}

func (client Client) removeAllowlistedIgnoredFiles(ctx context.Context, directory string) error {
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
	var unmatched []string
	var matched []string
	for _, name := range strings.Split(entries, "\x00") {
		if name == "" {
			continue
		}
		if matchesReturnIgnoredPath(name, client.ReturnIgnoredPaths) {
			matched = append(matched, name)
		} else {
			unmatched = append(unmatched, name)
		}
	}
	if len(unmatched) != 0 {
		return fmt.Errorf(
			"%w: return requires a clean checkout without ignored files; unmatched ignored paths: %q",
			ErrIdentity,
			unmatched,
		)
	}
	for _, name := range matched {
		// Remove individual ignored entries so tracked files inside a prefix remain intact.
		if err := root.Remove(name); err != nil {
			return err
		}
	}
	return nil
}
