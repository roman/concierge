package conciergecli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/tincan/pkg/inbox"
)

func TestVersionIncludesSourceRevision(t *testing.T) {
	previous := revision
	revision = "revision-b"
	t.Cleanup(func() { revision = previous })
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--version"}, nil, &output, io.Discard); code != 0 ||
		!strings.Contains(output.String(), "revision-b") {
		t.Fatalf("version code %d: %s", code, &output)
	}
}

func TestHostStatusReportsRevisionsAcrossPackages(t *testing.T) {
	for _, hostRevision := range []string{"dev", "revision-a", ""} {
		t.Run(hostRevision, func(t *testing.T) {
			t.Setenv("HERDR_SESSION", "")
			t.Setenv("TINCAN_HERDR", "")
			root := shortTaskRoot(t)
			messageRoot := filepath.Join(root, "messages")
			recipient, _ := inbox.ParseRecipient("concierge")
			executable, _ := os.Executable()
			serveConfiguredTaskRoot(t, hostservice.Config{
				Root: root, MessageRoot: messageRoot, Recipient: recipient,
				HerdrExecutable: unusedHerdr(t), Revision: hostRevision,
				WorkerCommands: taskops.CommandPaths{
					Concierge: "/old/concierge",
					Tincan:    executable,
				},
			})
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"--root", root, "--message-root", messageRoot, "host", "status"},
				nil,
				&output,
				io.Discard,
			)
			if code != 0 || !strings.Contains(output.String(), "binary_revision: \"dev\"") {
				t.Fatalf("status code %d: %s", code, &output)
			}
			if hostRevision == "" {
				hostRevision = "unknown"
			}
			if !strings.Contains(output.String(), "host_revision: "+quote(hostRevision)) ||
				!strings.Contains(output.String(), "upgrade_required: yes") {
				t.Fatalf("missing revision or package mismatch: %s", &output)
			}
		})
	}
}
