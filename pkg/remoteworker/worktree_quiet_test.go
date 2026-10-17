package remoteworker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeWorktreeQuietCheckWithOwnedProcess(t *testing.T) {
	executable := os.Getenv("LSOF_TEST_EXECUTABLE")
	if executable == "" {
		var err error
		executable, err = exec.LookPath("lsof")
		if err != nil {
			t.Skip("set LSOF_TEST_EXECUTABLE to verify the native process probe")
		}
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := checkWorktreeQuiet(ctx, executable, directory); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("sleep", "60")
	process.Dir = directory
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	if err := checkWorktreeQuiet(ctx, executable, directory); !errors.Is(err, ErrWorktreeBusy) {
		t.Fatalf("missed a process in the worktree: %v", err)
	}
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Wait()
	if err := checkWorktreeQuiet(ctx, executable, directory); err != nil {
		t.Fatalf("owned process departure was not confirmed: %v", err)
	}
}

func TestNativeWorktreeQuietCheckRefusesBusyAndUnconfirmedResults(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{name: "quiet", body: "exit 1"},
		{name: "busy", body: "printf 'p123\\n'; exit 0", want: ErrWorktreeBusy},
		{name: "warning", body: "printf 'inspection denied\\n' >&2; exit 1", want: ErrWorktreeQuietUnconfirmed},
		{name: "failed", body: "exit 2", want: ErrWorktreeQuietUnconfirmed},
		{name: "incomplete", body: "exit 0", want: ErrWorktreeQuietUnconfirmed},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			canonical, err := filepath.EvalSymlinks(directory)
			if err != nil {
				t.Fatal(err)
			}
			tool := filepath.Join(t.TempDir(), "lsof")
			script := "#!/bin/sh\n[ \"$1 $2 $3 $4 $5 $6\" = '-F p -a -d cwd +D' ] || exit 72\n[ \"$7\" = '" + canonical + "' ] || exit 72\n" + test.body + "\n"
			if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := checkWorktreeQuiet(context.Background(), tool, canonical); !errors.Is(
				err,
				test.want,
			) {
				t.Fatalf("quiet check: got %v, want %v", err, test.want)
			}
			linked := filepath.Join(t.TempDir(), "foreign")
			if err := os.Symlink(canonical, linked); err != nil {
				t.Fatal(err)
			}
			if err := checkWorktreeQuiet(context.Background(), tool, linked); !errors.Is(
				err,
				ErrWorktreeOwnership,
			) {
				t.Fatalf("symbolic worktree reached the process probe: %v", err)
			}
		})
	}
}
