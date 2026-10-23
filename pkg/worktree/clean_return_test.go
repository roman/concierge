package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCleanReturnResetsTrackedWorkAndRemovesAllExtraFiles(t *testing.T) {
	client, specification := checkoutForTest(t)
	if err := client.Prepare(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specification.Project, ".git", "info", "exclude"), []byte("ignored/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file.txt", "staged", "staged-missing", "untracked\nfile", "ignored/output"} {
		path := filepath.Join(specification.Path, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("pending"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitForTest(t, specification.Path, "add", "staged", "staged-missing")
	if err := os.Remove(filepath.Join(specification.Path, "staged-missing")); err != nil {
		t.Fatal(err)
	}
	var removed []string
	client.RemovedPath = func(path string) { removed = append(removed, path) }
	if err := client.CleanReturn(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	if status := gitForTest(t, specification.Path, "status", "--short", "--ignored"); status != "" {
		t.Fatalf("dirty checkout: %q", status)
	}
	want := []string{
		filepath.Join(specification.Path, "ignored"),
		filepath.Join(specification.Path, "untracked\nfile"),
		filepath.Join(specification.Path, "staged"),
	}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed paths = %q, want %q", removed, want)
	}
	if head := gitForTest(t, specification.Path, "rev-parse", "HEAD"); head != specification.BaseCommit {
		t.Fatal("cleanup moved HEAD")
	}
}

func TestCleanReturnRejectsOversizedReportBeforeMutation(t *testing.T) {
	client, specification := checkoutForTest(t)
	if err := client.Prepare(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	for index := range 256 {
		name := fmt.Sprintf("%03d-%s", index, strings.Repeat("x", 160))
		if err := os.WriteFile(filepath.Join(specification.Path, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracked := filepath.Join(specification.Path, "file.txt")
	if err := os.WriteFile(tracked, []byte("preserve edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.CleanReturn(t.Context(), specification); err == nil ||
		!strings.Contains(err.Error(), "24 KiB") {
		t.Fatalf("cleanup: %v", err)
	}
	if data, err := os.ReadFile(tracked); err != nil || string(data) != "preserve edits" {
		t.Fatalf("tracked work: %q %v", data, err)
	}
	entries, err := os.ReadDir(specification.Path)
	if err != nil || len(entries) < 257 {
		t.Fatalf("checkout files lost: %d %v", len(entries), err)
	}
}

func TestCleanReturnReportsDirectoryReplacedByTrackedFile(t *testing.T) {
	client, specification := checkoutForTest(t)
	if err := client.Prepare(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(specification.Path, "file.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "untracked"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var removed []string
	client.RemovedPath = func(path string) { removed = append(removed, path) }
	if err := client.CleanReturn(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{path}) {
		t.Fatalf("removed directories: %q", removed)
	}
}

func TestCleanReturnRejectsWrongIdentityBeforeRemovingFiles(t *testing.T) {
	client, specification := checkoutForTest(t)
	if err := client.Prepare(t.Context(), specification); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(specification.Path, "keep")
	if err := os.WriteFile(path, []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	specification.Branch = "foreign"
	if err := client.CleanReturn(t.Context(), specification); err == nil {
		t.Fatal("cleaned foreign checkout")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
