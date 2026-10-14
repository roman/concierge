package conciergecli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectShowOperatorSettings(t *testing.T) {
	for _, test := range []struct {
		name, config, repo, merge, delivery string
		code                                int
	}{
		{"absent", "", "repo", "off", "pr", 0},
		{"unconfigured project", `{"other":{"merge":"on","delivery":"fast-forward"}}`, "repo", "off", "pr", 0},
		{"autonomous PR", `{"repo":{"merge":"on"}}`, "repo", "on", "pr", 0},
		{"approved local", `{"repo":{"delivery":"fast-forward"}}`, "repo", "off", "fast-forward", 0},
		{"invalid merge", `{"repo":{"merge":"yes"}}`, "repo", "", "", 1},
		{"invalid delivery", `{"repo":{"delivery":"push"}}`, "repo", "", "", 1},
		{"unknown field", `{"repo":{"yolo":"on"}}`, "repo", "", "", 1},
		{"trailing JSON", `{} {}`, "repo", "", "", 1},
		{"null settings", `null`, "repo", "", "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.config != "" {
				if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "projects.json"), []byte(test.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			code := Run(
				context.Background(),
				[]string{"--root", root, "project", "show", test.repo},
				nil,
				&output,
				io.Discard,
			)
			if code != test.code {
				t.Fatalf("code %d: %s", code, &output)
			}
			if code == 0 {
				want := fmt.Sprintf(
					"repo: %q\nmerge: %s\ndelivery: %s\n",
					test.repo,
					test.merge,
					test.delivery,
				)
				if output.String() != want {
					t.Fatalf("want %q, got %q", want, output.String())
				}
			}
		})
	}
}
