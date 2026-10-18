package hostservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

func configureShipBridge(t *testing.T, server *Server, input LaunchRequest) {
	t.Helper()
	pool := os.Getenv("TREEHOUSE_TEST_EXECUTABLE")
	if pool == "" {
		t.Skip("set TREEHOUSE_TEST_EXECUTABLE to the pinned treehouse binary")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	for _, arguments := range [][]string{
		{"clone", "--bare", input.Project, origin},
		{"-C", input.Project, "config", "url." + origin + ".insteadOf", "ssh://git.test/project.git"},
		{"-C", input.Project, "update-ref", "refs/remotes/origin/main", input.BaseCommit},
		{"-C", input.Project, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	} {
		if data, err := exec.Command(server.config.GitExecutable, arguments...).CombinedOutput(); err != nil {
			t.Fatalf("ship bridge origin: %s %v", data, err)
		}
	}
	service, err := taskops.NewWithWorkerTools(
		server.config.Root,
		server.config.Recipient,
		taskops.WorkerTools{
			Commands: server.config.WorkerCommands,
			Git:      worktree.Client{Executable: server.config.GitExecutable},
			Pool:     worktree.PoolClient{Executable: pool, Root: t.TempDir()},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server.service = service.WithRemoteLeases(hostRemoteLeases{server: server}).
		WithRemoteResultExporter(hostRemoteResultExporter{server: server})
}

func commitShipBridgeResult(t *testing.T, server *Server, worker taskstate.Worker) {
	t.Helper()
	command := exec.Command(
		server.config.GitExecutable,
		"-C",
		worker.Worktree,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Remote ship result",
	)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native ship commit: %s %v", data, err)
	}
}

func landShipBridgeResult(
	t *testing.T,
	server *Server,
	input LaunchRequest,
	original taskstate.Worker,
) {
	t.Helper()
	current, err := server.store.Read(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	worker := current.Snapshot().Worker
	result := worker.RemoteResult
	if result.Phase != taskstate.RemoteResultReady || result.Lease.State != taskstate.LeaseHeld ||
		result.Lease.Project != input.Project || worker.Lease != original.Lease || worker.Worktree != original.Worktree {
		t.Fatal("ship acceptance lost the native binding or primary result lease")
	}
	if data, err := exec.Command(server.config.GitExecutable, "-C", input.Project, "push", "origin", result.Commit+":refs/heads/main").CombinedOutput(); err != nil {
		t.Fatalf("primary fixture landing: %s %v", data, err)
	}
}
