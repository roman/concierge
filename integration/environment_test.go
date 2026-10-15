package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCallerStateRootsRemainUntouched(t *testing.T) {
	decoy := t.TempDir()
	for _, name := range []string{"CONCIERGE_HOME", "TINCAN_HOME"} {
		root := filepath.Join(decoy, name)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, root)
	}
	herdr := filepath.Join(decoy, "herdr")
	marker := filepath.Join(decoy, "herdr-called")
	if err := os.WriteFile(herdr, []byte("#!/bin/sh\ntouch \"$TEST_DECOY_HERDR_MARKER\"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TINCAN_HERDR", herdr)
	t.Setenv("HERDR_SESSION", "decoy-session")
	t.Setenv("TEST_DECOY_HERDR_MARKER", marker)

	// A separate process exercises package setup with the caller's environment.
	command := exec.Command("go", "test", "./pkg/conciergecli", "./integration",
		"-skip=^TestCallerStateRootsRemainUntouched$")
	command.Dir = ".."
	output, err := command.CombinedOutput()
	for _, name := range []string{"CONCIERGE_HOME", "TINCAN_HOME"} {
		entries, readErr := os.ReadDir(os.Getenv(name))
		if readErr != nil || len(entries) != 0 {
			t.Errorf("caller %s was changed: %v, %v", name, entries, readErr)
		}
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("caller Herdr executable was invoked: %v", statErr)
	}
	if err != nil {
		t.Fatalf("tests with caller state roots: %v\n%s", err, output)
	}
}
