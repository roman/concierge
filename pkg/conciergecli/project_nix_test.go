package conciergecli

import (
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectNixDevelopArguments(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, "", "project", "set", "repo", "--merge=on", "--branch=v3")
	want := []string{"--impure", "--option", "setting", "value with spaces"}
	arguments := []string{"project", "set", "repo"}
	for _, argument := range want {
		arguments = append(arguments, "--nix-develop-arg="+argument)
	}
	invoke(t, root, "", arguments...)
	path := filepath.Join(root, "config", "projects.json")
	settings, err := readProjectSettings(path, "repo")
	if err != nil || !reflect.DeepEqual(settings.NixDevelopArgs, want) || settings.Branch != "v3" ||
		settings.Merge != "on" {
		t.Fatalf("settings: %+v %v", settings, err)
	}
	for _, invalid := range [][]string{
		{"--nix-develop-arg="},
		{"--nix-develop-arg=bad\nargument"},
		{"--nix-develop-arg=--impure", "--clear-nix-develop-args"},
	} {
		var output bytes.Buffer
		args := append([]string{"--root", root, "project", "set", "repo"}, invalid...)
		if code := Run(t.Context(), args, nil, &output, io.Discard); code == 0 {
			t.Fatalf("invalid setting accepted: %v", invalid)
		}
		settings, err := readProjectSettings(path, "repo")
		if err != nil || !reflect.DeepEqual(settings.NixDevelopArgs, want) {
			t.Fatalf("invalid update changed settings: %+v %v", settings, err)
		}
	}
	invoke(t, root, "", "project", "set", "repo", "--clear-nix-develop-args")
	settings, err = readProjectSettings(path, "repo")
	if err != nil || len(settings.NixDevelopArgs) != 0 || settings.Branch != "v3" {
		t.Fatalf("clear arguments: %+v %v", settings, err)
	}
}
