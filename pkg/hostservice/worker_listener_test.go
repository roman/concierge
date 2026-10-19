package hostservice

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
	"github.com/roman/tincan/pkg/inbox"
)

func workerHost(t *testing.T, version int) *Server {
	t.Helper()
	directory, err := os.MkdirTemp("", "worker-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	root := filepath.Join(directory, "state")
	if err := taskstate.Initialize(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if version == 3 {
		if err := taskstate.UpgradeVersionTwo(context.Background(), root, filepath.Join(directory, "backup")); err != nil {
			t.Fatal(err)
		}
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	server, err := New(Config{Root: root, MessageRoot: filepath.Join(directory, "messages"),
		Recipient: recipient, HerdrExecutable: "/tools/herdr", Diagnostics: io.Discard,
		WorkerCommands: taskops.CommandPaths{
			Concierge: "/tools/concierge",
			Tincan:    "/tools/tincan",
		}})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestHostOwnsSeparateWorkerListenerForVersionThree(t *testing.T) {
	server := workerHost(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := server.Listen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	workerPath := filepath.Join(server.config.Root, "worker.sock")
	workerInfo, err := os.Lstat(workerPath)
	if err != nil || workerInfo.Mode().Perm() != 0600 {
		t.Fatalf("worker listener is unavailable or has unsafe permissions: %v", err)
	}
	hostInfo, err := os.Lstat(filepath.Join(server.config.Root, "host.sock"))
	if err != nil || os.SameFile(hostInfo, workerInfo) {
		t.Fatalf("worker and host listeners are not separate: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener.UnixListener) }()
	request := workerservice.Request{
		Task:       "task-1",
		Generation: "gen-1",
		Secret:     "incorrect",
		Operation:  workeraccess.ListPending,
	}
	if _, err := workerservice.Call(ctx, workerPath, request); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) {
		t.Fatalf("worker listener did not apply grant authorization: %v", err)
	}
	if err := server.ensureTaskWorkerSocket(request.Task); err != nil {
		t.Fatal(err)
	}
	taskPath := remoteworker.PrimaryWorkerSocketPath(server.config.Root, request.Task)
	if info, err := os.Lstat(taskPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("task listener unavailable: %v", err)
	}
	server.closeTaskWorkerSocket(request.Task)
	if _, err := os.Lstat(taskPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retirement retained task listener: %v", err)
	}
	if _, err := workerservice.Call(ctx, workerPath, request); !errors.Is(
		err,
		workeraccess.ErrDenied,
	) {
		t.Fatalf("task listener retirement stopped the host service: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("host shutdown left the worker listener running")
	}
	if _, err := os.Lstat(workerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shutdown retained the worker socket: %v", err)
	}
}

func TestVersionTwoHostRetainsLocalListenerBehavior(t *testing.T) {
	server := workerHost(t, 2)
	listener, err := server.Listen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := os.Lstat(filepath.Join(server.config.Root, "worker.sock")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("local root created a worker socket: %v", err)
	}
}

func TestOccupiedWorkerPathLeavesHostLeaseRecoverable(t *testing.T) {
	server := workerHost(t, 3)
	path := filepath.Join(server.config.Root, "worker.sock")
	if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if listener, err := server.Listen(context.Background()); err == nil {
		listener.Close()
		t.Fatal("host replaced an occupied worker socket path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "retain" {
		t.Fatalf("occupied worker path changed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen(context.Background())
	if err != nil {
		t.Fatalf("failed worker bind retained the host lease: %v", err)
	}
	listener.Close()
}
