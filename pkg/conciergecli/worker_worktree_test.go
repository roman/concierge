package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerWorktreeHelperRejectsInvalidInputBeforeOpeningPrimaryPaths(t *testing.T) {
	for _, operation := range []string{"acquire", "import", "prepare-launch", "return", "inspect"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			root, messages := filepath.Join(
				directory,
				"primary-state",
			), filepath.Join(
				directory,
				"primary-messages",
			)
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"--root", root, "--message-root", messages, "worker-worktree", operation},
				strings.NewReader(`{"unknown":true}`),
				&output,
				&output,
			)
			if code != 2 ||
				!strings.Contains(output.String(), "original bounded worktree request") {
				t.Fatalf("invalid helper input: %d %s", code, &output)
			}
			for _, path := range []string{root, messages} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("opened primary paths during native setup")
				}
			}
		})
	}
}

func TestRemoteWorkerCannotAcquireItsOwnWorktree(t *testing.T) {
	t.Setenv("CONCIERGE_WORKER_SOCKET", "/remote/worker.sock")
	for _, operation := range []string{"acquire", "import", "prepare-launch", "return", "inspect"} {
		t.Run(operation, func(t *testing.T) {
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"worker-worktree", operation},
				strings.NewReader(`{}`),
				&output,
				&output,
			)
			if code == 0 || strings.Contains(output.String(), "original bounded worktree request") {
				t.Fatalf("remote worker reached native setup: %d %s", code, &output)
			}
		})
	}
}
