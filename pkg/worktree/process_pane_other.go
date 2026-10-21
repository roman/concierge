//go:build !linux

package worktree

func processPaneHint(int) string { return "unavailable" }
