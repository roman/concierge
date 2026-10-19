package remoteworker

import (
	"strings"
	"testing"
)

func TestPrimarySocketPathLimitsAndExistingLiveRoot(t *testing.T) {
	for _, test := range []struct {
		platform  string
		rootBytes int
		wantError bool
	}{
		{"darwin", 63, false}, {"darwin", 64, true},
		{"linux", 67, false}, {"linux", 68, true},
	} {
		root := "/" + strings.Repeat("p", test.rootBytes-1)
		err := validatePrimaryWorkerSocketPath(root, "task-1", test.platform)
		if (err != nil) != test.wantError {
			t.Fatalf("%s root %d: %v", test.platform, test.rootBytes, err)
		}
		if err != nil &&
			(!strings.Contains(err.Error(), "bytes") || !strings.Contains(err.Error(), "--root")) {
			t.Fatal(err)
		}
	}
	for _, platform := range []string{"linux", "darwin"} {
		if err := validatePrimaryWorkerSocketPath("/home/roman/Projects/self/exmachina/.concierge", "remote-hardening", platform); err != nil {
			t.Fatal(err)
		}
	}
}
