package conciergecli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestDiscardRequiresAuthorityBeforeHostCall(t *testing.T) {
	var output bytes.Buffer
	code := Run(
		t.Context(),
		[]string{"--root", t.TempDir(), "task", "discard", "task-1", "--generation", "gen-1"},
		nil,
		&output,
		io.Discard,
	)
	if code != 2 || !strings.Contains(output.String(), "operator-approved") {
		t.Fatalf("code %d: %s", code, &output)
	}
}

func TestDiscardRequiresPrimaryHostAdmission(t *testing.T) {
	for _, mode := range []string{"missing", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			root := lifecycleRoot(t)
			if mode == "unauthorized" {
				serveTaskRoot(t, root)
			}
			var output bytes.Buffer
			code := Run(
				t.Context(),
				[]string{
					"--root",
					root,
					"task",
					"discard",
					"task-1",
					"--generation",
					"gen-1",
					"--operator-approved",
				},
				nil,
				&output,
				io.Discard,
			)
			if code != 1 {
				t.Fatalf("code %d: %s", code, &output)
			}
		})
	}
}
