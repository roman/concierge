package conciergecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentProjectSettings(t *testing.T) {
	root := t.TempDir()
	var workers sync.WaitGroup
	for index := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var output bytes.Buffer
			args := []string{"--root", root, "project", "set", fmt.Sprint(index), "--merge", "on"}
			if code := Run(context.Background(), args, nil, &output, io.Discard); code != 0 {
				t.Errorf("code %d: %s", code, &output)
			}
		}()
	}
	workers.Wait()
	projects, err := readProjects(filepath.Join(root, "config", "projects.json"))
	if err != nil || len(projects) != 12 {
		t.Fatalf("lost updates: %v %v", projects, err)
	}
}

type failedSettingsWriter struct{}

func (failedSettingsWriter) Write([]byte) (int, error) { return 0, errors.New("output closed") }

func TestBriefIncludeReportsOutputFailure(t *testing.T) {
	if code := Run(context.Background(), []string{"--root", t.TempDir(), "brief-include", "clear"}, nil, failedSettingsWriter{}, io.Discard); code != 1 {
		t.Fatalf("code: %d", code)
	}
}

func TestProjectSet(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, "", "project", "set", "other", "--merge", "on")
	invoke(t, root, "", "project", "set", "repo", "--delivery", "fast-forward")
	invoke(t, root, "", "project", "set", "repo", "--merge", "on")
	for _, repo := range []string{"repo", "other"} {
		got := invoke(t, root, "", "project", "show", repo)
		if !strings.Contains(got, "merge: on\n") {
			t.Fatal(got)
		}
	}
	if got := invoke(t, root, "", "project", "show", "repo"); !strings.Contains(
		got,
		"delivery: fast-forward",
	) {
		t.Fatal(got)
	}
	for _, flags := range [][]string{nil, {"--merge", "yes"}, {"--delivery", "push"}} {
		expectLifecycleError(t, root, 2, append([]string{"project", "set", "repo"}, flags...)...)
	}
	path := filepath.Join(root, "config", "projects.json")
	if err := os.WriteFile(path, []byte(`{"other":{"merge":"invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	expectLifecycleError(t, root, 1, "project", "set", "repo", "--merge", "on")
	data, _ := os.ReadFile(path)
	if string(data) != `{"other":{"merge":"invalid"}}` {
		t.Fatal("invalid configuration was replaced")
	}
}

func TestBriefIncludeSetAndClear(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "instructions.md")
	content := "  Use these defaults.\n"
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(t, root, "", "brief-include", "set", "--file", file)
	got, err := appendBriefInclude(root, "Work")
	if err != nil || !strings.HasSuffix(got, content) {
		t.Fatalf("include: %q %v", got, err)
	}
	for range 2 {
		invoke(t, root, "", "brief-include", "clear")
	}
	got, err = appendBriefInclude(root, "Work")
	if err != nil || got != "Work" {
		t.Fatalf("cleared include: %q %v", got, err)
	}
	expectLifecycleError(t, root, 1, "brief-include", "set", "--file", file+"-missing")
	expectLifecycleError(t, root, 1, "brief-include", "set", "--file", filepath.Dir(file))
}
