package taskstate

import "testing"

func TestDisplayName(t *testing.T) {
	for _, test := range []struct{ repo, id, want string }{
		{"tincan", "tincan-cleanup", "tincan/cleanup"},
		{"zoo.nix", "agent-sandbox-config", "zoo.nix/agent-sandbox-config"},
		{"tincan", "other-tincan-cleanup", "tincan/other-tincan-cleanup"},
	} {
		t.Run(test.id, func(t *testing.T) {
			if got := (Definition{Repo: test.repo, ID: test.id}).DisplayName(); got != test.want {
				t.Fatalf("display name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLaunchLabelValidation(t *testing.T) {
	for _, label := range []string{"tincan/cleanup", "concierge-gen-1"} {
		t.Run(label, func(t *testing.T) {
			task, err := NewQueued(
				Definition{ID: "task-1", Repo: "tincan", Title: "Work", Kind: "task"},
			)
			if err != nil {
				t.Fatal(err)
			}
			intent := launchForTest()
			intent.Label = label
			if _, err := task.ReserveInitialLaunch(intent); err != nil {
				t.Fatal(err)
			}
			intent.Name = "tincan/worker"
			if _, err := task.ReserveInitialLaunch(intent); err == nil {
				t.Fatal("agent name accepted slash")
			}
		})
	}
}
