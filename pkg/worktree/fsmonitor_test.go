package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectionsDoNotRunConfiguredFsmonitor(t *testing.T) {
	client, specification := checkoutForTest(t)
	marker := filepath.Join(t.TempDir(), "fsmonitor-started")
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf '\\0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	gitForTest(t, specification.Project, "config", "core.fsmonitor", hook)
	// Confirm this Git supports the hook before checking Concierge's inspections.
	gitForTest(t, specification.Project, "status", "--porcelain")
	if err := os.Remove(marker); err != nil {
		t.Fatalf("fsmonitor hook did not run in the control: %v", err)
	}
	for _, arguments := range [][]string{
		{"status", "--porcelain=v1", "--ignored"},
		{"ls-files", "--others", "--ignored", "--exclude-standard"},
	} {
		t.Run(arguments[0], func(t *testing.T) {
			t.Cleanup(func() { _ = os.Remove(marker) })
			if _, err := client.git(t.Context(), specification.Project, arguments...); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("inspection ran the configured fsmonitor: %v", err)
			}
		})
	}
	if got := gitForTest(t, specification.Project, "config", "core.fsmonitor"); got != hook {
		t.Fatalf("inspection changed repository configuration: %q", got)
	}
}
