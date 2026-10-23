package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStopFsmonitorAcceptsOnlyKnownAbsentDaemonResponses(t *testing.T) {
	for _, test := range []struct {
		name     string
		message  string
		exitCode int
		wantErr  bool
	}{
		{"not running", "fatal: fsmonitor--daemon is not running", 128, false},
		{"unsupported", "fatal: fsmonitor--daemon not supported on this platform", 128, false},
		{"other error", "fatal: permission denied", 128, true},
		{"wrong exit", "fatal: fsmonitor--daemon not supported on this platform", 1, true},
		{"extra diagnostic", "fatal: fsmonitor--daemon not supported on this platform\nerror: cleanup failed", 128, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapper := filepath.Join(t.TempDir(), "git")
			script := fmt.Sprintf(
				"#!/bin/sh\ncase \"$*\" in\n*'config --get core.fsmonitor') echo true;;\n*'fsmonitor--daemon stop') echo '%s' >&2; exit %d;;\n*) exit 2;;\nesac\n",
				test.message,
				test.exitCode,
			)
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			client := Client{Executable: wrapper}
			err := client.StopFsmonitor(t.Context(), t.TempDir())
			if (err != nil) != test.wantErr {
				t.Fatalf("StopFsmonitor: %v; want error=%t", err, test.wantErr)
			}
		})
	}
}

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
