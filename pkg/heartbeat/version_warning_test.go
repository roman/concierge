package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/versioncheck"
	"github.com/roman/tincan/pkg/inbox"
)

func TestCollectIncludesPendingVersionWarnings(t *testing.T) {
	root := initializedRoot(t)
	if err := os.Mkdir(filepath.Join(root, "records"), 0700); err != nil {
		t.Fatal(err)
	}
	messageRoot := t.TempDir()
	recipient, _ := inbox.ParseRecipient("concierge")
	report := versioncheck.Report{
		Warnings: []versioncheck.Warning{
			{
				Side:      "machine",
				Component: "herdr-server",
				Detail:    "server_binary_stale: yes",
				Step:      "Restart Herdr on baker.",
			},
		},
	}
	if err := versioncheck.Publish(messageRoot, recipient, root, "task-1", report); err != nil {
		t.Fatal(err)
	}
	if err := versioncheck.Publish(messageRoot, recipient, root, "task-1", report); err != nil {
		t.Fatal(err)
	}
	observation, err := Collect(t.Context(), root, messageRoot, recipient.String(), "/unused")
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Replies) != 1 || !observation.Notify ||
		!strings.Contains(observation.Text, "server_binary_stale") ||
		!strings.Contains(observation.Text, "Restart Herdr on baker.") {
		t.Fatalf("missing warning: %+v", observation)
	}
	messages, _ := inbox.New(messageRoot)
	pending, _ := messages.List(recipient)
	if err := messages.Acknowledge(recipient, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	observation, err = Collect(t.Context(), root, messageRoot, recipient.String(), "/unused")
	if err != nil || strings.Contains(observation.Text, "server_binary_stale") {
		t.Fatalf("repeated acknowledged warning: %+v %v", observation, err)
	}
}
