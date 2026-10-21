package conciergecli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestPreviewReport(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		outcome   string
		sections  []string
		decisions []string
	}{
		{
			name:      "handoff",
			body:      "## Findings\nDetailed evidence.\n## Handoff\n### Outcome\nThe fix works.\nChecks pass.\n\nExtra detail.\n### Current state\nReady.\n## Gaps\n### Missing fixture\nDetails.\n## Learnings\n### Cache behavior\nDetails.\n## Artifacts\n### Test log\nDetails.\n## Open decisions\n- Choose a release date.\n  More detail.\n\n- Approve the rollout.\n",
			outcome:   "The fix works. Checks pass.",
			sections:  []string{"Gaps", "Missing fixture", "Learnings", "Cache behavior", "Artifacts", "Test log"},
			decisions: []string{"- Choose a release date.", "- Approve the rollout."},
		},
		{
			name: "opaque text",
			body: "Complete result\nwith evidence",
		},
		{
			name:      "fences and unrelated outcomes",
			body:      "## Example\n### Outcome\nWrong outcome.\n```md\n## Gaps\n## Open decisions\nWrong decision.\n```\n## Handoff\n### Outcome\nCorrect outcome.\n## Open decisions\n### Deployment\nAsk the operator.\n\nMore evidence.\n### Cleanup\nKeep the checkout?\n",
			outcome:   "Correct outcome.",
			decisions: []string{"Deployment: Ask the operator.", "Cleanup: Keep the checkout?"},
		},
		{
			name:      "standalone outcome and nested decision",
			body:      "## Outcome\nReady.\n## Handoff\n### Next actions\n#### Open questions\nCan we deploy?\n",
			outcome:   "Ready.",
			decisions: []string{"Can we deploy?"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview := previewReport(test.body)
			if preview.Outcome != test.outcome ||
				strings.Join(preview.Sections, "|") != strings.Join(test.sections, "|") ||
				strings.Join(preview.Decisions, "|") != strings.Join(test.decisions, "|") {
				t.Fatalf("preview: %#v", preview)
			}
		})
	}
}

func TestConciseReplyAndReport(t *testing.T) {
	t.Setenv(workerLaunchGeneration, "gen-1")
	root := lifecycleRoot(t)
	startFixtureTask(t, root)
	messages := t.TempDir()
	assignment := assignmentMessage(t, root, messages, "gen-1", "Work")
	body := "## Handoff\n### Outcome\nReady ✓.\n### Current state\nSECRET DETAILS\n## Gaps\n### Follow-up\nMore details.\n## Open decisions\nApprove deployment?\n"
	invoke(
		t,
		root,
		body,
		"--message-root",
		messages,
		"task",
		"reply",
		"send",
		"task-1",
		"--generation",
		"gen-1",
		"--key",
		"preview",
		"--assignment-id",
		assignment.ID.String(),
		"--body-file",
		"-",
	)
	mailbox, _ := inbox.New(messages)
	primary, _ := inbox.ParseRecipient("concierge")
	replies, err := mailbox.List(primary)
	if err != nil || len(replies) != 1 {
		t.Fatalf("replies: %v %v", replies, err)
	}
	id := replies[0].ID.String()
	check := []string{"--message-root", messages, "task", "reply", "check", id}
	for _, command := range [][]string{check, {"task", "report", "task-1", id}} {
		got := invoke(t, root, "", append(command, "--format", "concise")...)
		for _, want := range []string{"task: \"task-1\"", "generation: \"gen-1\"", "reply_id: " + quote(id), fmt.Sprintf("body_bytes: %d", len(body)), "outcome: \"Ready ✓.\"", "\"Follow-up\"", "\"Approve deployment?\""} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q: %s", want, got)
			}
		}
		if strings.Contains(got, "SECRET") || strings.Contains(got, "body:") {
			t.Fatalf("full body leaked: %s", got)
		}
		if got := invoke(t, root, "", append(command, "--format", "toon")...); !strings.Contains(
			got,
			"SECRET DETAILS",
		) {
			t.Fatalf("missing full body: %s", got)
		}
	}
	service, err := taskops.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finish(context.Background(), "task-1", "gen-1", taskstate.Close, "2026-10-20"); err != nil {
		t.Fatal(err)
	}
	store, _ := taskstate.NewStore(root)
	if _, err := store.Update(context.Background(), "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.RetireResult("gen-1") }); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"concise", "toon"} {
		if got := invoke(t, root, "", append(check, "--format", format)...); got != "status: stale\ntask: \"task-1\"\n" {
			t.Fatalf("stale leak: %s", got)
		}
	}
}

func TestReportPreviewLimitsAndWriterFailure(t *testing.T) {
	preview := previewReport("## Handoff\n### Outcome\n" + strings.Repeat("✓", 601) +
		"\n## Gaps\n### " + strings.Repeat("é", 121) +
		"\n## Open decisions\n" + strings.Repeat("界", 241))
	for _, test := range []struct {
		text  string
		limit int
	}{
		{preview.Outcome, 600}, {preview.Sections[1], 120}, {preview.Decisions[0], 240},
	} {
		if !utf8.ValidString(test.text) || utf8.RuneCountInString(test.text) != test.limit+1 ||
			!strings.HasSuffix(test.text, "…") {
			t.Fatalf("invalid shortened preview: %q", test.text)
		}
	}
	if err := writeReportPreview(failedOutput{}, "reply", "Body", "body"); err == nil {
		t.Fatal("lost output error")
	}
}
