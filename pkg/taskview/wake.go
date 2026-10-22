package taskview

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
)

type wakeSummary struct {
	kind, task, preview string
}

// Summaries are navigation aids. Only reply check validates a reply's binding.
func summarizeWake(body string) wakeSummary {
	fallback := wakeSummary{kind: "unknown", preview: body}
	var envelope struct {
		Kind        string   `json:"kind"`
		Task        string   `json:"task"`
		Body        string   `json:"body"`
		NativeState string   `json:"observed_state"`
		Screen      string   `json:"observed_screen"`
		ScreenError string   `json:"screen_error"`
		Checkpoint  string   `json:"checkpoint"`
		Replies     []string `json:"preserved_replies"`
		Assignments []string `json:"redelivered_assignments"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return fallback
	}
	if _, err := taskmeta.ParseTaskID(envelope.Task); err != nil {
		return fallback
	}
	var preview string
	switch envelope.Kind {
	case "task-reply", "task-assignment":
		preview = envelope.Body
	case "task-stalled":
		preview = envelope.NativeState
		for _, detail := range []string{envelope.ScreenError, envelope.Screen} {
			if detail != "" {
				preview += ": " + detail
				break
			}
		}
	case "task-native-checkpoint":
		preview = envelope.Checkpoint
	case "task-restart":
		preview = fmt.Sprintf(
			"%d preserved replies; %d redelivered assignments",
			len(envelope.Replies),
			len(envelope.Assignments),
		)
	case "task-bootstrap":
		preview = "Worker bootstrap"
	default:
		return fallback
	}
	return wakeSummary{
		kind:    envelope.Kind,
		task:    envelope.Task,
		preview: strings.Join(strings.Fields(preview), " "),
	}
}
