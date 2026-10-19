package machineprep

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestSlotTrustReadsNativeConfigWithoutChanges(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("Nix is required for the remote TOML reader")
	}
	for _, test := range []struct{ name, config, want string }{
		{"trusted", "trust_level = \"trusted\"", "ready"},
		{"untrusted", "trust_level = \"untrusted\"", "missing"},
		{"missing", "", "missing"},
		{"trusted parent", "trust_level = \"trusted\"", "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			slot := filepath.Join(base, "worktrees", ".treehouse", "repo-id", "1", "repo")
			if err := os.MkdirAll(slot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(slot, ".git"), []byte("gitdir: /fixture/metadata\n"), 0600); err != nil {
				t.Fatal(err)
			}
			codexHome := t.TempDir()
			configuredPath := slot
			if test.name == "trusted parent" {
				configuredPath = base
			}
			configuration := "[projects." + strconv.Quote(
				configuredPath,
			) + "]\n" + test.config + "\n"
			configPath := filepath.Join(codexHome, "config.toml")
			if err := os.WriteFile(configPath, []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			checks, err := (Preparer{Runner: localPreparation{environment: []string{"CODEX_HOME=" + codexHome}}}).InspectSlotTrust(
				t.Context(),
				testMachine(),
				Configuration{
					Target: testMachine().Target,
					Base:   base,
					Agent:  "codex",
					Repositories: []Repository{
						{Name: "concierge", URL: "ssh://git.test/concierge.git"},
					},
				},
			)
			if err != nil || len(checks) != 1 {
				t.Fatalf("slot trust inspection: %+v %v", checks, err)
			}
			if checks[0].Detail != slot || checks[0].Status != test.want {
				t.Fatalf("wrong slot trust: %+v", checks[0])
			}
			if test.want != "ready" &&
				!strings.Contains(checks[0].Step, "codex -C "+sshcommand.Quote(slot)) {
				t.Fatalf("missing exact native step: %+v", checks[0])
			}
			data, err := os.ReadFile(configPath)
			if err != nil || string(data) != configuration {
				t.Fatal("trust inspection changed native configuration")
			}
			entries, err := os.ReadDir(codexHome)
			if err != nil || len(entries) != 1 {
				t.Fatalf("trust inspection wrote native state: %v %v", entries, err)
			}
		})
	}
}

func TestSlotTrustWithNoAllocatedSlotsDoesNotBlockPreparation(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("Nix is required for the remote TOML reader")
	}
	base := t.TempDir()
	checks, err := (Preparer{Runner: localPreparation{environment: []string{"CODEX_HOME=" + t.TempDir()}}}).InspectSlotTrust(
		t.Context(),
		testMachine(),
		Configuration{
			Target: testMachine().Target, Base: base, Agent: "codex", Repositories: []Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
		},
	)
	if err != nil || len(checks) != 1 {
		t.Fatalf("empty pool inspection: %+v %v", checks, err)
	}
	if !(Report{Checks: checks}).Ready() || !strings.Contains(checks[0].Detail, "0 allocated") ||
		!strings.Contains(checks[0].Detail, base+"/worktrees") ||
		!strings.Contains(checks[0].Step, "first remote task launch") {
		t.Fatalf("empty pool blocked preparation or implied slot trust: %+v", checks)
	}
}
