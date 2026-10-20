package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeReadinessRequiresSynchronousHooks(t *testing.T) {
	for _, trial := range []struct {
		name       string
		settings   string
		wantError  bool
		removeHook string
	}{
		{"missing", "", true, ""},
		{"empty", `{}`, true, ""},
		{"async activity", claudeSettings(`,"async":true`), true, ""},
		{"async Stop only", strings.Replace(claudeSettings(""), `activity; fi"},{`, `activity; fi","async":true},{`, 1), true, ""},
		{"missing prompt", strings.Replace(claudeSettings(""), "UserPromptSubmit", "Ignored", 1), true, ""},
		{"prompt can fail open", strings.Replace(claudeSettings(""), `,"onFailure":"block"`, "", 1), true, ""},
		{"missing consumer", strings.Replace(claudeSettings(""), "concierge-claude-stop", "other", 1), true, ""},
		{"short consumer", strings.Replace(claudeSettings(""), "28800", "15", 1), true, ""},
		{"missing activity file", claudeSettings(""), true, "concierge-claude-activity"},
		{"missing consumer file", claudeSettings(""), true, "concierge-claude-stop"},
		{"ready", claudeSettings(""), false, ""},
	} {
		t.Run(trial.name, func(t *testing.T) {
			project := t.TempDir()
			if err := os.MkdirAll(filepath.Join(project, ".agents", "hooks"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"concierge-claude-activity", "concierge-claude-stop"} {
				if err := os.WriteFile(filepath.Join(project, ".agents", "hooks", name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if trial.removeHook != "" {
				if err := os.Remove(filepath.Join(project, ".agents", "hooks", trial.removeHook)); err != nil {
					t.Fatal(err)
				}
			}
			if trial.settings != "" {
				if err := os.Mkdir(filepath.Join(project, ".claude"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, ".claude", "settings.json"), []byte(trial.settings), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := ValidateClaudeHooks(project); (err != nil) != trial.wantError {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func claudeSettings(activityOptions string) string {
	return `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-activity; fi","onFailure":"block"` + activityOptions + `}]}],"Stop":[{"hooks":[{"type":"command","command":"if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-activity; fi"` + activityOptions + `},{"type":"command","command":"if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-stop; fi","asyncRewake":true,"timeout":28800}]}]}}`
}
