package conciergecli

import "testing"

func TestLandingGitDisablesFsmonitor(t *testing.T) {
	got, err := landingGitCommand(t.Context(), t.TempDir(), "config", "--get", "core.fsmonitor")
	if err != nil || got != "false" {
		t.Fatalf("effective core.fsmonitor = %q: %v", got, err)
	}
}
