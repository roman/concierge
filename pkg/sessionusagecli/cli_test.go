package sessionusagecli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{
		{"json", []string{"--harness=codex", "--sessions=../sessionusage/testdata/codex", "--project=/project", "--from=2026-10-20T22:00:00Z", "--to=2026-10-20T23:00:00Z", "--format=json"}, 0, `"input": 300`},
		{"toon", []string{"--harness=claude", "--sessions=../sessionusage/testdata/claude", "--project=/project", "--from=2026-10-20T22:00:00Z", "--to=2026-10-20T23:00:00Z"}, 0, "primary,2,310,180,130,30,12"},
		{"unknown", []string{"--bogus"}, 2, "error:"},
		{"bad time", []string{"--from=yesterday", "--to=2026-10-20T23:00:00Z"}, 2, "error:"},
		{"conflict", []string{"--from=2026-10-20T22:00:00Z", "1h"}, 2, "error:"},
		{"no window", nil, 2, "error:"},
		{"missing directory", []string{"--sessions=/missing-session-fixture", "1h"}, 1, "error:"},
		{"version", []string{"--version"}, 0, "dev"},
		{"help", []string{"--help"}, 0, "--harness"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr)
			if code != test.code || !strings.Contains(stdout.String(), test.contains) {
				t.Fatalf("code %d, output %s, stderr %s", code, &stdout, &stderr)
			}
			if test.name == "json" && !json.Valid(stdout.Bytes()) {
				t.Fatal("invalid JSON")
			}
		})
	}
}

func TestWindow(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		command   command
		from, to  time.Time
		wantError bool
	}{
		{"duration", command{Duration: "6h"}, now.Add(-6 * time.Hour), now, false},
		{"anchored duration", command{Duration: "1h", To: "2026-10-20T23:00:00Z"}, now.Add(-2 * time.Hour), now.Add(-time.Hour), false},
		{"negative", command{Duration: "-1h"}, time.Time{}, time.Time{}, true},
		{"reversed", command{From: "2026-10-21T01:00:00Z", To: "2026-10-20T23:00:00Z"}, time.Time{}, time.Time{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			from, to, err := test.command.window(now)
			if (err != nil) != test.wantError {
				t.Fatal(err)
			}
			if err == nil && (!from.Equal(test.from) || !to.Equal(test.to)) {
				t.Fatalf("window %s to %s", from, to)
			}
		})
	}
}

func TestTOONStringEscapes(t *testing.T) {
	for _, value := range []string{"<project>", "a&b", "line\nnext", "quoted\"path", "path\\name"} {
		quoted := quote(value)
		if strings.Contains(quoted, `\u003c`) || strings.Contains(quoted, `\u003e`) ||
			strings.Contains(quoted, `\u0026`) {
			t.Errorf("TOON string uses JSON-only escapes: %s", quoted)
		}
		var decoded string
		if err := json.Unmarshal([]byte(quoted), &decoded); err != nil || decoded != value {
			t.Fatalf("quote %q = %s", value, quoted)
		}
	}
}
