package conciergecli

import (
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectReturnIgnoredPaths(t *testing.T) {
	root := t.TempDir()
	want := []string{".claude/rules/", ".locker/", ".codex/config.toml"}
	args := []string{"project", "set", "repo", "--merge=on"}
	for _, path := range want {
		args = append(args, "--return-ignored-path="+path)
	}
	invoke(t, root, "", args...)
	path := filepath.Join(root, "config", "projects.json")
	settings, err := readProjectSettings(path, "repo")
	if err != nil || !reflect.DeepEqual(settings.ReturnIgnoredPaths, want) {
		t.Fatalf("settings: %+v %v", settings, err)
	}
	var output bytes.Buffer
	if code := Run(t.Context(), []string{"--root", root, "project", "show", "repo"}, nil, &output, io.Discard); code != 0 ||
		!strings.Contains(output.String(), `return_ignored_paths[3]: ".claude/rules/",".locker/",".codex/config.toml"`) {
		t.Fatalf("show: %d %s", code, &output)
	}
	for _, flags := range [][]string{
		{"--return-ignored-path=../private"}, {"--return-ignored-path=.git/"},
		{"--return-ignored-path="}, {"--return-ignored-path=*"},
		{"--return-ignored-path=generated/", "--clear-return-ignored-paths"},
	} {
		output.Reset()
		args := append([]string{"--root", root, "project", "set", "repo"}, flags...)
		if code := Run(t.Context(), args, nil, &output, io.Discard); code == 0 {
			t.Fatalf("accepted %q", flags)
		}
		settings, err := readProjectSettings(path, "repo")
		if err != nil || !reflect.DeepEqual(settings.ReturnIgnoredPaths, want) {
			t.Fatalf("invalid update changed settings: %+v %v", settings, err)
		}
	}
	invoke(t, root, "", "project", "set", "repo", "--clear-return-ignored-paths")
	settings, err = readProjectSettings(path, "repo")
	if err != nil || len(settings.ReturnIgnoredPaths) != 0 || settings.Merge != "on" {
		t.Fatalf("clear: %+v %v", settings, err)
	}
}
