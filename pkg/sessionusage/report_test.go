package sessionusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSavedReports(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			report, err := Measure(
				Options{
					Harness:   harness,
					Directory: filepath.Join("testdata", harness),
					Project:   "/project",
					From:      parseTime(t, "2026-10-20T22:00:00Z"),
					To:        parseTime(t, "2026-10-20T23:00:00Z"),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join("testdata", harness+"-expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var expected Totals
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(report.Primary.Usage, expected) {
				t.Fatalf("usage = %+v, want %+v", report.Primary.Usage, expected)
			}
			if len(report.Sessions) != 1 {
				t.Fatalf("sessions = %d", len(report.Sessions))
			}
			if report.Primary.Turns != 2 || report.Primary.WakeTurns != 1 {
				t.Fatalf("turns = %+v", report.Primary)
			}
			if report.Primary.WakeUsage.Requests != 1 {
				t.Fatalf("wake usage = %+v", report.Primary.WakeUsage)
			}
			if report.Primary.Compactions != 1 || report.Primary.SkillReads != 2 {
				t.Fatalf("context metrics = %+v", report.Primary)
			}
			if report.Primary.ToolOutput["claim"].Bytes != 5 {
				t.Fatalf("claim = %+v", report.Primary.ToolOutput)
			}
			if report.Primary.SkippedRecords != 1 {
				t.Fatalf("skipped = %d", report.Primary.SkippedRecords)
			}
		})
	}
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
