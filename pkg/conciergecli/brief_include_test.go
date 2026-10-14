package conciergecli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAppendBriefInclude(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{"absent", "", "Task-specific instructions."},
		{"empty", "", "Task-specific instructions."},
		{"whitespace", " \n\t", "Task-specific instructions."},
		{"verbatim", "  Operator defaults.\n", "Task-specific instructions.\n\n## Standing worker instructions\n\nTask-specific instructions take precedence over these defaults.\n\n  Operator defaults.\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.name != "absent" {
				if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "brief-include.md"), []byte(test.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := appendBriefInclude(root, "Task-specific instructions.")
			if err != nil || got != test.want {
				t.Fatalf("include = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestConfigurationRejectsNonregularFiles(t *testing.T) {
	for _, kind := range []string{"directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if kind == "fifo" {
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := readOptionalConfiguration(path); err == nil {
				t.Fatal("accepted a nonregular configuration file")
			}
		})
	}
}
