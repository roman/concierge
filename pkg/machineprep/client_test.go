package machineprep

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientBuildImportsExactUnpublishedCommitAndPreservesSource(t *testing.T) {
	origin := t.TempDir()
	source := filepath.Join(origin, "concierge.git")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, source, "init", "-b", "main")
	commitFixture(t, source, "published")
	configuration := Configuration{
		Target:       "baker",
		Base:         filepath.Join(t.TempDir(), "remote owner's base"),
		Agent:        "codex",
		Repositories: []Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
	}
	runner := localPreparation{origin: origin}
	preparer := Preparer{Runner: runner}
	if err := preparer.PrepareRepositories(context.Background(), testMachine(), configuration); err != nil {
		t.Fatal(err)
	}
	commitFixture(t, source, "unpublished")
	commit := gitCommand(t, source, "rev-parse", "HEAD")
	bundlePath := filepath.Join(t.TempDir(), "source.bundle")
	gitCommand(t, source, "bundle", "create", bundlePath, "HEAD")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(configuration.Base, "repositories", "concierge.git")
	if exec.Command("git", "-C", bare, "cat-file", "-e", commit).Run() == nil {
		t.Fatal("fixture already contains unpublished commit")
	}
	fakeBin := t.TempDir()
	fakeClient := filepath.Join(t.TempDir(), "client")
	if err := os.MkdirAll(filepath.Join(fakeClient, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeClient, "bin", "concierge"), []byte("#!/bin/sh\nprintf 'test client\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	nixLog := filepath.Join(t.TempDir(), "nix-arguments")
	if err := os.WriteFile(filepath.Join(fakeBin, "nix"), []byte(`#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$FAKE_NIX_LOG"
while [ "$#" -gt 0 ]; do
    if [ "$1" = --out-link ]; then shift; output=$1; fi
    shift
done
if [ ! -L "$output" ]; then ln -s "$FAKE_CLIENT" "$output"; fi
`), 0700); err != nil {
		t.Fatal(err)
	}
	runner.environment = []string{
		"PATH=" + fakeBin + ":" + os.Getenv("PATH"),
		"FAKE_CLIENT=" + fakeClient,
		"FAKE_NIX_LOG=" + nixLog,
	}
	preparer.Runner = runner
	for range 2 {
		client, err := preparer.BuildClient(
			context.Background(),
			testMachine(),
			configuration,
			commit,
			bytes.NewReader(bundle),
		)
		if err != nil {
			t.Fatal(err)
		}
		if client.Commit != commit ||
			client.Executable != filepath.Join(
				configuration.Base,
				"clients",
				commit,
				"bin",
				"concierge",
			) {
			t.Fatalf("wrong client: %+v", client)
		}
	}
	arguments, err := os.ReadFile(nixLog)
	if err != nil || !strings.Contains(string(arguments), "rev="+commit) ||
		!strings.Contains(string(arguments), "--no-write-lock-file") {
		t.Fatalf("unpinned client build: %s %v", arguments, err)
	}
	checkout := filepath.Join(configuration.Base, "client-sources", commit)
	if got := gitCommand(t, checkout, "rev-parse", "HEAD"); got != commit {
		t.Fatal("built a different source commit")
	}
	if err := os.WriteFile(filepath.Join(checkout, "keep"), []byte("operator data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.BuildClient(context.Background(), testMachine(), configuration, commit, bytes.NewReader(bundle)); err == nil {
		t.Fatal("built from dirty client source")
	}
	data, err := os.ReadFile(filepath.Join(checkout, "keep"))
	if err != nil || string(data) != "operator data" {
		t.Fatal("changed existing source edits")
	}
	if _, err := preparer.BuildClient(context.Background(), testMachine(), configuration, strings.Repeat("a", 40), bytes.NewReader(bundle)); err == nil {
		t.Fatal("accepted bundle for a different commit")
	}
}

func TestClientBuildRejectsInvalidCommitBeforeSSH(t *testing.T) {
	runner := &probeRunner{t: t}
	configuration := Configuration{
		Target:       "baker",
		Base:         "/remote/base",
		Agent:        "codex",
		Repositories: []Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
	}
	for _, commit := range []string{"HEAD", "-option", strings.Repeat("a", 39), strings.Repeat("A", 40)} {
		if _, err := (Preparer{Runner: runner}).BuildClient(context.Background(), testMachine(), configuration, commit, strings.NewReader("bundle")); err == nil {
			t.Fatal("accepted mutable or invalid commit")
		}
	}
	if len(runner.commands) != 0 {
		t.Fatal("invalid client source reached SSH")
	}
}
