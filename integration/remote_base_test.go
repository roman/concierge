package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
)

func TestAcceptancePrimaryDirectoryIgnoresDeepTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), strings.Repeat("deep", 25)))
	directory, err := newAcceptancePrimaryDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if err := remoteworker.ValidatePrimaryWorkerSocketPath(filepath.Join(directory, "state"), "remote-acceptance-scout"); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(directory) != "/tmp" && filepath.Dir(directory) != "/private/tmp" {
		t.Fatalf("unexpected primary parent: %s", directory)
	}
}

func TestFixedAcceptanceBaseKeepsPoolAndExcludesOtherRuns(t *testing.T) {
	commands := t.TempDir()
	if err := os.WriteFile(filepath.Join(commands, "lsof"), []byte("#!/bin/sh\nif [ -n \"$TEST_ACCEPTANCE_BUSY\" ]; then printf 'p123\\n'; exit 0; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	data, err := exec.Command("mktemp", "-d", "/tmp/ca.XXXXXX").Output()
	if err != nil {
		t.Fatal(err)
	}
	directory := strings.TrimSpace(string(data))
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	claim := func(owner, machine, origin string) error {
		output, err := exec.Command("sh", "-c", claimFixedAcceptanceBase, "acceptance", directory, owner, machine, origin).
			CombinedOutput()
		if err == nil && strings.TrimSpace(string(output)) != directory {
			t.Fatalf("claim path: %q", output)
		}
		return err
	}
	if err := claim("first", "machine", "git@example.invalid:repo.git"); err != nil {
		t.Fatal(err)
	}
	if err := claim("other", "machine", "git@example.invalid:repo.git"); err == nil {
		t.Fatal("concurrent run entered the pool")
	}
	pool := filepath.Join(directory, "work", "worktrees", "trusted-slot")
	if err := os.MkdirAll(pool, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sh", "-c", releaseFixedAcceptanceBase, "acceptance", directory, "other").CombinedOutput(); err == nil {
		t.Fatalf("foreign release: %s", output)
	}
	t.Setenv("TEST_ACCEPTANCE_BUSY", "1")
	if output, err := exec.Command("sh", "-c", releaseFixedAcceptanceBase, "acceptance", directory, "first").CombinedOutput(); err == nil {
		t.Fatalf("released a busy base: %s", output)
	}
	t.Setenv("TEST_ACCEPTANCE_BUSY", "")
	if output, err := exec.Command("sh", "-c", releaseFixedAcceptanceBase, "acceptance", directory, "first").CombinedOutput(); err != nil {
		t.Fatalf("release: %s %v", output, err)
	}
	if _, err := os.Stat(pool); err != nil {
		t.Fatal("release discarded a trusted slot")
	}
	if err := claim("second", "other-machine", "git@example.invalid:repo.git"); err == nil {
		t.Fatal("accepted another machine")
	}
	if err := claim("second", "machine", "git@example.invalid:other.git"); err == nil {
		t.Fatal("accepted another repository")
	}
	if err := claim("second", "machine", "git@example.invalid:repo.git"); err != nil {
		t.Fatalf("same pool cannot be reused: %v", err)
	}
}

func TestFixedAcceptanceBaseRejectsUnmarkedOrLinkedDirectory(t *testing.T) {
	for _, mode := range []string{"unmarked", "linked"} {
		t.Run(mode, func(t *testing.T) {
			data, err := exec.Command("mktemp", "-d", "/tmp/ca.XXXXXX").Output()
			if err != nil {
				t.Fatal(err)
			}
			directory := strings.TrimSpace(string(data))
			t.Cleanup(func() { _ = os.RemoveAll(directory) })
			if mode == "linked" {
				if err := os.Remove(directory); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), directory); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := exec.Command("sh", "-c", claimFixedAcceptanceBase, "acceptance", directory, "owner", "machine", "origin").CombinedOutput(); err == nil {
				t.Fatalf("claimed %s base: %s", mode, output)
			}
		})
	}
}

const claimFixedAcceptanceBase = `set -eu
umask 077
directory=$1
owner=$2
machine=$3
origin=$4
case "${directory%/*}" in /tmp|/private/tmp) ;; *) exit 1 ;; esac
case "${directory##*/}" in ca.??????) ;; *) exit 1 ;; esac
expected=$(printf '%s\n' concierge-acceptance-pool-v1 "$machine" "$origin")
if [ ! -e "$directory" ] && [ ! -L "$directory" ]; then
 mkdir -m 700 "$directory"
 printf '%s\n' "$expected" > "$directory/.acceptance-pool"
fi
[ -d "$directory" ] && [ ! -L "$directory" ] && [ -O "$directory" ] || exit 1
[ "$(cd -P "$directory" && pwd)" = "$directory" ] || exit 1
[ "$(LC_ALL=C ls -ld "$directory" | cut -c 1-10)" = drwx------ ] || exit 1
[ -f "$directory/.acceptance-pool" ] && [ ! -L "$directory/.acceptance-pool" ] || exit 1
[ "$(cat "$directory/.acceptance-pool")" = "$expected" ] || exit 1
mkdir -m 700 "$directory/.acceptance-active" || {
 printf 'Acceptance base is already claimed. Inspect its retained run before retrying.\n' >&2
 exit 1
}
printf '%s\n' "$owner" > "$directory/.acceptance-active/owner"
printf '%s\n' "$directory"
`

const releaseFixedAcceptanceBase = `set -eu
directory=$1
owner=$2
[ -d "$directory" ] && [ ! -L "$directory" ] && [ -O "$directory" ] || exit 1
[ "$(cd -P "$directory" && pwd)" = "$directory" ] || exit 1
[ -f "$directory/.acceptance-pool" ] && [ ! -L "$directory/.acceptance-pool" ] || exit 1
active="$directory/.acceptance-active"
[ -d "$active" ] && [ ! -L "$active" ] || exit 1
[ -f "$active/owner" ] && [ ! -L "$active/owner" ] || exit 1
[ "$(cat "$active/owner")" = "$owner" ] || exit 1
if [ -e "$directory/work/machine-owner" ]; then
 [ -f "$directory/work/machine-owner" ] && [ ! -L "$directory/work/machine-owner" ] || exit 1
 [ "$(cat "$directory/work/machine-owner")" = "$(sed -n '2p' "$directory/.acceptance-pool")" ] || exit 1
fi
` + quietAcceptanceDirectory + `
rm -- "$active/owner"
rmdir -- "$active"
`

const quietAcceptanceDirectory = `
if [ -d "$directory/work" ]; then
 find "$directory/work" -name .git -prune -print | while IFS= read -r metadata; do
  checkout=${metadata%/.git}
  case "$checkout" in "$directory/work/"*) ;; *) exit 1 ;; esac
  if [ "$(git -C "$checkout" config --get core.fsmonitor || true)" = true ]; then
   git -C "$checkout" fsmonitor--daemon stop
  fi
 done
fi
set +e
output=$(lsof +D "$directory" 2>&1)
status=$?
set -e
[ "$status" = 1 ] && [ -z "$output" ] || exit 1
`
