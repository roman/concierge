package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteReturnIgnoredPaths(t *testing.T) {
	for _, test := range []struct {
		name      string
		paths     []string
		extra     string
		wantError string
	}{
		{name: "approved directory", paths: []string{"generated/"}},
		{name: "approved file", paths: []string{"generated/output"}},
		{name: "unmatched ignored file", paths: []string{"generated/"}, extra: "unknown", wantError: "unknown"},
		{name: "tracked change", paths: []string{"generated/"}, extra: "tracked", wantError: "clean checkout"},
		{name: "staged change", paths: []string{"generated/"}, extra: "staged", wantError: "clean checkout"},
		{name: "untracked file", paths: []string{"generated/"}, extra: "untracked", wantError: "clean checkout"},
		{name: "missing list", wantError: "generated/output"},
		{name: "empty list", paths: []string{}, wantError: "generated/output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, request := remoteReturnFixture(t)
			request.ReturnIgnoredPaths = test.paths
			writeRemoteIgnoredOutput(t, manager, &request)
			if test.extra != "" {
				name := test.extra
				if name == "tracked" || name == "staged" {
					name = ".gitignore"
				}
				if err := os.WriteFile(filepath.Join(request.Lease.Path, name), []byte("retain this work"), 0600); err != nil {
					t.Fatal(err)
				}
				if test.extra == "staged" {
					remoteProjectGit(t, manager.Git.Executable, request.Lease.Path, "add", name)
				}
			}
			returned, err := manager.Return(context.Background(), request)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("return: %v, want %q", err, test.wantError)
				}
				if _, err := os.Stat(filepath.Join(request.Lease.Path, "generated/output")); err != nil {
					t.Fatalf("refused return removed output: %v", err)
				}
				return
			}
			if err != nil || returned.State != taskstate.LeaseReturned {
				t.Fatalf("return: %+v %v", returned, err)
			}
		})
	}
}

func TestRemoteReturnRetriesWithUpdatedIgnoredPaths(t *testing.T) {
	manager, request := remoteReturnFixture(t)
	writeRemoteIgnoredOutput(t, manager, &request)
	if _, err := manager.Return(t.Context(), request); err == nil {
		t.Fatal("returned ignored output without approval")
	}
	request.ReturnIgnoredPaths = []string{"generated/"}
	returned, err := manager.Return(t.Context(), request)
	if err != nil || returned.State != taskstate.LeaseReturned {
		t.Fatalf("retry: %+v %v", returned, err)
	}
}

func TestRemoteReturnGuardsPrecedeIgnoredRemoval(t *testing.T) {
	for _, guard := range []string{"lease", "proof", "process", "invalid path"} {
		t.Run(guard, func(t *testing.T) {
			manager, request := remoteReturnFixture(t)
			writeRemoteIgnoredOutput(t, manager, &request)
			request.ReturnIgnoredPaths = []string{"generated/"}
			switch guard {
			case "lease":
				request.Lease.ID = "foreign"
			case "proof":
				request.Proof = WorktreeReturnProof{
					Outcome: WorktreeLanded,
					Commit:  request.Worktree.Location.ClientCommit,
				}
			case "process":
				manager.CheckQuiet = func(context.Context, string) error { return ErrWorktreeBusy }
			case "invalid path":
				request.ReturnIgnoredPaths = []string{"../"}
			}
			if _, err := manager.Return(t.Context(), request); err == nil {
				t.Fatal("accepted unsafe return")
			}
			if _, err := os.Stat(filepath.Join(request.Lease.Path, "generated/output")); err != nil {
				t.Fatalf("guard removed ignored file: %v", err)
			}
		})
	}
}

func writeRemoteIgnoredOutput(t *testing.T, manager Worktrees, request *WorktreeReturnRequest) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(request.Lease.Path, ".gitignore"), []byte("generated/\nunknown\n"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteProjectGit(t, manager.Git.Executable, request.Lease.Path, "add", ".gitignore")
	remoteProjectGit(
		t,
		manager.Git.Executable,
		request.Lease.Path,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"-m",
		"Ignore generated outputs",
	)
	request.Proof = WorktreeReturnProof{
		Outcome: WorktreeAbandoned,
		Commit: remoteProjectGit(
			t,
			manager.Git.Executable,
			request.Lease.Path,
			"rev-parse",
			"HEAD",
		),
	}
	if err := os.Mkdir(filepath.Join(request.Lease.Path, "generated"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.Lease.Path, "generated/output"), []byte("regenerable"), 0600); err != nil {
		t.Fatal(err)
	}
}
