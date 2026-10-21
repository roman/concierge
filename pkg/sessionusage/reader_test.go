package sessionusage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLargeRecordsAndUnattributedRecords(t *testing.T) {
	directory := t.TempDir()
	data := `{"type":"session_meta","payload":{"id":"p","cwd":"/project"}}
{"type":"future_record"}
{"type":"response_item","timestamp":"2026-10-20T22:00:00Z","payload":{"type":"message","role":"assistant","content":[{"text":"` + strings.Repeat("x", 1024*1024) + `"}]}}
{"type":"token_usage_record","timestamp":"2026-10-20T22:30:00Z","payload":{"response_id":"r","turn_id":"one","usage":{"input_tokens":50}}}`
	if err := os.WriteFile(filepath.Join(directory, "session.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Measure(
		Options{
			Harness:   "codex",
			Directory: directory,
			Project:   "/project",
			From:      parseTime(t, "2026-10-20T22:00:00Z"),
			To:        parseTime(t, "2026-10-20T23:00:00Z"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Primary.Usage.Input != 50 || report.UnattributedRecords != 1 {
		t.Fatalf("usage %v, unattributed %d", report.Primary.Usage, report.UnattributedRecords)
	}
}

func TestEscalationArguments(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int64
	}{
		{`{"sandbox_permissions":"require_escalated","cmd":"true"}`, 1},
		{`await tools.exec_command({sandbox_permissions: "require_escalated"})`, 1},
		{map[string]any{"commands": []any{map[string]any{"sandbox_permissions": "require_escalated"}}}, 1},
		{`{"cmd":"echo require_escalated"}`, 0},
	} {
		if got := escalationCount(test.value); got != test.want {
			t.Errorf("%v: got %d, want %d", test.value, got, test.want)
		}
	}
}

func TestNonzeroResults(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  int64
	}{
		{"success", `{"exit_code":0,"output":"failure mentioned in text"}`, 0},
		{"decoded", `{"exit_code":7,"output":""}`, 1},
		{"native", "Process exited with code 2\nOutput:", 1},
		{"wrapped", []any{map[string]any{"text": `{"exit_code":1}`}}, 1},
		{"ordinary text", "example exit_code: 3", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nonzeroResults(test.value); got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}

func TestWindowAndChildren(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{
		"primary.jsonl": `{"type":"session_meta","payload":{"id":"p","cwd":"/project"}}
{"type":"token_usage_record","timestamp":"2026-10-20T21:00:00Z","payload":{"response_id":"outside","turn_id":"old","usage":{"input_tokens":800}}}
{"type":"token_usage_record","timestamp":"2026-10-20T22:00:00Z","payload":{"response_id":"outside","turn_id":"old","usage":{"input_tokens":800}}}
{"type":"token_usage_record","timestamp":"2026-10-20T22:00:00Z","payload":{"response_id":"r","turn_id":"one","usage":{"input_tokens":100}}}
{"type":"token_usage_record","timestamp":"2026-10-20T23:00:00Z","payload":{"response_id":"s","turn_id":"two","usage":{"input_tokens":200}}}
{"type":"token_usage_record","timestamp":"2026-10-20T23:00:01Z","payload":{"response_id":"late","usage":{"input_tokens":400}}}
broken
`,
		"child.jsonl": `{"type":"session_meta","payload":{"id":"c","parent_thread_id":"p","cwd":"/project"}}
{"type":"token_usage_record","timestamp":"2026-10-20T22:30:00Z","payload":{"response_id":"r","turn_id":"one","usage":{"input_tokens":50}}}
`,
		"other.jsonl": `{"type":"session_meta","payload":{"id":"other","cwd":"/elsewhere"}}
{"type":"token_usage_record","timestamp":"2026-10-20T22:30:00Z","payload":{"response_id":"r","usage":{"input_tokens":900}}}
`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Measure(
		Options{
			Harness:   "codex",
			Directory: directory,
			Project:   "/project",
			From:      parseTime(t, "2026-10-20T22:00:00Z"),
			To:        parseTime(t, "2026-10-20T23:00:00Z"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Primary.Usage.Input != 300 || report.Children.Usage.Input != 50 ||
		report.UnattributedRecords != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCodexFixtureDetails(t *testing.T) {
	report, err := Measure(
		Options{
			Harness:   "codex",
			Directory: "testdata/codex",
			Project:   "/project",
			From:      parseTime(t, "2026-10-20T22:00:00Z"),
			To:        parseTime(t, "2026-10-20T23:00:00Z"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if *report.Primary.RequireEscalated != 1 {
		t.Fatalf("escalations = %d", *report.Primary.RequireEscalated)
	}
	if !reflect.DeepEqual(report.Sessions[0].SkillReadsBetweenCompactions, []int64{2, 0}) {
		t.Fatal(report.Sessions[0].SkillReadsBetweenCompactions)
	}
	if report.Primary.WeeklyQuota.First.UsedPercent != 20 {
		t.Fatal(report.Primary.WeeklyQuota)
	}
}

func TestClaudeFinalUsageSnapshot(t *testing.T) {
	s := newReaderState(Options{Harness: "claude"}, sessionIdentity{id: "p"})
	s.claude(
		object{
			"type": "assistant",
			"message": map[string]any{
				"id":    "r",
				"usage": map[string]any{"output_tokens": float64(1)},
			},
		},
		withinWindow,
	)
	s.claude(
		object{
			"type": "assistant",
			"message": map[string]any{
				"id":    "r",
				"usage": map[string]any{"output_tokens": float64(9)},
			},
		},
		withinWindow,
	)
	session := s.finish()
	if session.Usage.Requests != 1 || session.Usage.Output != 9 {
		t.Fatalf("usage = %+v", session.Usage)
	}
}
