package remoteworker

import (
	"os/exec"
	"testing"

	"github.com/roman/concierge/pkg/worktree"
)

func TestProjectGitDisablesFsmonitor(t *testing.T) {
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	manager := Worktrees{Git: worktree.Client{Executable: executable}}
	got, err := manager.projectGit(t.Context(), t.TempDir(), "config", "--get", "core.fsmonitor")
	if err != nil || got != "false" {
		t.Fatalf("effective core.fsmonitor = %q: %v", got, err)
	}
}
