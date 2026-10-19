package machineprep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBaseIsCanonicalAndReadOnly(t *testing.T) {
	for _, test := range []struct {
		name     string
		symbolic bool
		blocked  bool
	}{
		{"physical", false, false}, {"symbolic prefix", true, false}, {"non-directory prefix", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			prefix := filepath.Join(parent, "physical")
			if test.blocked {
				if err := os.WriteFile(prefix, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(prefix, 0700); err != nil {
				t.Fatal(err)
			}
			configured := filepath.Join(prefix, "new base")
			if test.symbolic {
				alias := filepath.Join(parent, "alias")
				if err := os.Symlink(prefix, alias); err != nil {
					t.Fatal(err)
				}
				configured = filepath.Join(alias, "new base")
			}
			resolved, err := (Preparer{Runner: localPreparation{}}).ResolveBase(
				t.Context(),
				testMachine(),
				"",
				configured,
			)
			if test.blocked {
				if err == nil || !strings.Contains(err.Error(), prefix) ||
					!strings.Contains(err.Error(), "not a directory") {
					t.Fatalf("missing actionable path error: %v", err)
				}
				return
			}
			canonical, err := filepath.EvalSymlinks(prefix)
			if err != nil {
				t.Fatal(err)
			}
			if resolved != filepath.Join(canonical, "new base") {
				t.Fatalf("base=%q want canonical prefix %q", resolved, canonical)
			}
			if _, err := os.Stat(filepath.Join(prefix, "new base")); !os.IsNotExist(err) {
				t.Fatalf("resolution wrote the base: %v", err)
			}
		})
	}
}

func TestMachineConfigurationAllowsStableDefaultBase(t *testing.T) {
	data := []byte(
		`{"machine-id":{"target":"dev-remote","agent":"codex","repositories":[{"name":"concierge","url":"ssh://git.test/concierge.git"}]}}`,
	)
	configuration, err := DecodeConfiguration(data, testMachine())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := (Preparer{Runner: localPreparation{}}).ResolveBase(
		t.Context(),
		testMachine(),
		"",
		configuration.Base,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !validBase(resolved) || !strings.HasSuffix(resolved, "/.local/share/concierge") {
		t.Fatalf("unstable default: %q", resolved)
	}
	second, err := (Preparer{Runner: localPreparation{}}).ResolveBase(
		t.Context(),
		testMachine(),
		"",
		configuration.Base,
	)
	if err != nil || second != resolved {
		t.Fatalf("default changed between runs: %q %q %v", resolved, second, err)
	}
}
