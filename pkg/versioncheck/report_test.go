package versioncheck

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompareComponents(t *testing.T) {
	ready := Snapshot{
		Concierge:          "abc",
		HerdrClient:        "0.9.5",
		HerdrServer:        "0.9.5",
		Agent:              "codex-cli 1.0",
		Stale:              "no",
		PrivateCompatible:  "yes",
		EndpointCompatible: "yes",
	}
	for _, test := range []struct {
		name    string
		change  func(*Snapshot)
		warning string
		blocked bool
	}{
		{"same", func(*Snapshot) {}, "", false},
		{"commit", func(s *Snapshot) { s.Concierge = "def" }, "concierge", false},
		{"client", func(s *Snapshot) { s.HerdrClient = "0.9.6" }, "herdr-client", false},
		{"server", func(s *Snapshot) { s.HerdrServer = "0.9.0" }, "herdr-server", false},
		{"agent", func(s *Snapshot) { s.Agent = "codex-cli 2.0" }, "codex", false},
		{"stale", func(s *Snapshot) { s.Stale = "yes" }, "server_binary_stale", false},
		{"unknown", func(s *Snapshot) { s.HerdrServer = "" }, "unconfirmed", false},
		{"private protocol", func(s *Snapshot) { s.PrivateCompatible = "no" }, "private_protocol", true},
		{"endpoint protocol", func(s *Snapshot) { s.EndpointCompatible = "no" }, "endpoint_protocol", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := ready
			test.change(&remote)
			report := Compare(ready, &remote, "codex", "baker")
			var output bytes.Buffer
			if err := report.Write(&output); err != nil {
				t.Fatal(err)
			}
			if report.Blocked() != test.blocked {
				t.Fatalf("blocked=%v: %s", report.Blocked(), &output)
			}
			if test.warning == "" && len(report.Warnings) != 0 {
				t.Fatal(output.String())
			}
			if test.warning != "" &&
				(!strings.Contains(output.String(), test.warning) || !strings.Contains(output.String(), "help")) {
				t.Fatal(output.String())
			}
			for _, value := range []string{"primary", "machine", "abc", "0.9.5", "codex-cli 1.0"} {
				if !strings.Contains(output.String(), value) {
					t.Fatalf("missing %s: %s", value, &output)
				}
			}
		})
	}
}

func TestLocalStaleServerWarning(t *testing.T) {
	report := Compare(
		Snapshot{HerdrClient: "0.9.5", HerdrServer: "0.9.0", Stale: "yes"},
		nil,
		"claude",
		"",
	)
	var output bytes.Buffer
	if err := report.Write(&output); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"server_binary_stale", "primary", "herdr", "restart"} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("missing %s: %s", value, &output)
		}
	}
	if report.Blocked() {
		t.Fatal("plain stale server blocks launch")
	}
}
