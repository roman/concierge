package machineprep

import (
	"fmt"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

func TestMachineConfigurationResultLimit(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int64
		valid bool
	}{
		{"default", 0, true},
		{"configured", 4096, true},
		{"negative", -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(
				`{"machine":{"target":"remote","base":"/base","agent":"codex","max_result_bytes":%d,"repositories":[{"name":"concierge","url":"git@github.com:roman/concierge.git"}]}}`,
				test.limit,
			)
			_, err := DecodeConfiguration(
				[]byte(data),
				herdr.SavedMachine{ID: "machine", Target: "remote", Enabled: true},
			)
			if (err == nil) != test.valid {
				t.Fatalf("configuration validity: %v", err)
			}
		})
	}
}
