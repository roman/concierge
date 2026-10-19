package herdr

import (
	"context"
	"testing"
)

func TestMachineCatalogResolvesSavedIdentity(t *testing.T) {
	for _, test := range []struct {
		name, selector, body string
		valid                bool
	}{
		{"label", "dev-remote", `[{"id":"machine-1","label":"dev-remote","target":"user@dev-remote","session":"work","enabled":true}]`, true},
		{"identity", "machine-1", `[{"id":"machine-1","label":"dev-remote","target":"user@dev-remote","session":"work","enabled":true}]`, true},
		{"missing", "other", `[]`, false},
		{"disabled", "dev-remote", `[{"id":"machine-1","label":"dev-remote","target":"user@dev-remote","session":"work","enabled":false}]`, false},
		{"ambiguous", "dev-remote", `[{"id":"machine-1","label":"dev-remote","target":"user@dev-remote","session":"work","enabled":true},{"id":"machine-2","label":"dev-remote","target":"other","session":"work","enabled":true}]`, false},
		{"null", "dev-remote", `null`, false},
		{"wrong shape", "dev-remote", `{"machines":[]}`, false},
		{"duplicate identity", "machine-1", `[{"id":"machine-1","label":"dev-remote","target":"user@dev-remote","session":"work","enabled":true},{"id":"machine-1","label":"other","target":"other","session":"work","enabled":true}]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{responses: []runResponse{{stdout: []byte(test.body)}}}
			machine, err := (MachineCatalog{Runner: runner}).Resolve(
				context.Background(),
				test.selector,
			)
			if (err == nil) != test.valid {
				t.Fatalf("resolved %+v, error %v", machine, err)
			}
			if test.valid &&
				(machine.ID != "machine-1" || machine.Target != "user@dev-remote" || machine.Session != "work") {
				t.Fatalf("wrong saved identity: %+v", machine)
			}
			if len(runner.calls) != 1 || len(runner.calls[0]) != 3 ||
				runner.calls[0][0] != "machine" ||
				runner.calls[0][1] != "list" ||
				runner.calls[0][2] != "--json" {
				t.Fatalf("unexpected catalog lookup: %q", runner.calls)
			}
		})
	}
}
