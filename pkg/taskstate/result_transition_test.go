package taskstate

import (
	"strings"
	"testing"
)

func TestResultTransitionCanValidateBeforeRemotePreparation(t *testing.T) {
	task, err := remoteResultTask(
		t,
	).SelectRemoteResult("gen-1", strings.Repeat("c", 40), "primary-holder")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.CheckResultTransition("gen-1", Close, "2026-10-17"); err != nil {
		t.Fatal(err)
	}
	if _, err := task.AcceptResult("gen-1", Close, "2026-10-17"); err == nil {
		t.Fatal("preflight bypassed unfinished materialization")
	}
	for _, request := range []struct {
		generation string
		mode       CompletionMode
		date       string
	}{
		{"another-generation", Close, "2026-10-17"},
		{"gen-1", Close, "not-a-date"},
		{"gen-1", Retain, ""},
		{"gen-1", "unknown", ""},
	} {
		if err := task.CheckResultTransition(request.generation, request.mode, request.date); err == nil {
			t.Fatalf("invalid result request reached preparation: %+v", request)
		}
	}
	held, err := task.PlaceHold(Hold{Reason: "Follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	if err := held.CheckResultTransition("gen-1", Retain, ""); err != nil {
		t.Fatal(err)
	}
	if err := held.CheckResultTransition("gen-1", Close, "2026-10-17"); err == nil {
		t.Fatal("prepared a closing result while held")
	}
}
