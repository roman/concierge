package hostservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type hostTunnelProcesses struct{ starts, stops atomic.Int32 }

func (processes *hostTunnelProcesses) Start(
	context.Context,
	remoteworker.TunnelSpec,
	string,
) (primary.ProcessToken, error) {
	processes.starts.Add(1)
	return primary.ProcessToken{
		PID:          123,
		StartTicks:   "100",
		BootID:       "boot",
		PIDNamespace: "namespace",
	}, nil
}

func (*hostTunnelProcesses) Status(primary.ProcessToken) (primary.ProcessStatus, error) {
	return primary.ProcessRunning, nil
}

func (processes *hostTunnelProcesses) Stop(context.Context, primary.ProcessToken) error {
	processes.stops.Add(1)
	return nil
}

type hostTunnelRemote struct {
	probes atomic.Int32
	probe  func(context.Context) error
}

func (*hostTunnelRemote) Prepare(context.Context, remoteworker.TunnelSpec) error { return nil }

func (remote *hostTunnelRemote) Probe(ctx context.Context, _ remoteworker.TunnelSpec) error {
	remote.probes.Add(1)
	if remote.probe != nil {
		return remote.probe(ctx)
	}
	return nil
}

func hostTunnelFixture(
	t *testing.T,
) (*Server, remoteworker.TunnelSpec, *hostTunnelProcesses, *hostTunnelRemote) {
	t.Helper()
	server := workerHost(t, 3)
	grant, _, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskstate.NewMigrated(taskstate.Snapshot{
		Definition: *intakeRequest().Intake, State: taskstate.InFlight,
		Worker: &taskstate.Worker{Name: "worker", Harness: "codex", Machine: "machine-1",
			Session: "work", Pane: "pane-1", Workspace: "workspace-1", Tab: "tab-1",
			Project: "/remote/repo", Worktree: "/remote/worktree", Generation: "gen-1",
			AgentArgs: []string{"resume", "thread-1"}, Access: &grant},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := task.Encode()
	if err := os.WriteFile(filepath.Join(server.config.Root, "records", "task-1.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	spec := remoteworker.TunnelSpec{Owner: remoteworker.SocketOwner{
		Task: grant.Task, Generation: grant.Generation, Verifier: grant.Verifier},
		MachineID: "machine-1", Target: "synthetic-machine",
		Tools: remoteworker.Tools{Concierge: "/remote/client", TaskDirectory: "/remote/task"}}
	processes, remote := &hostTunnelProcesses{}, &hostTunnelRemote{}
	server.tunnels, err = remoteworker.NewTunnelManager(server.config.Root, processes, remote)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.tunnels.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	remote.probes.Store(0)
	return server, spec, processes, remote
}

func TestHostTunnelProbeChecksCurrentGrantBeforeExternalWork(t *testing.T) {
	server, _, processes, remote := hostTunnelFixture(t)
	if err := server.probeTaskTunnel(context.Background(), "task-1", "gen-1"); err != nil {
		t.Fatal(err)
	}
	if remote.probes.Load() != 1 {
		t.Fatal("current task could not probe its tunnel")
	}
	_, err := server.store.WithTask(
		context.Background(),
		"task-1",
		func(task *taskstate.LockedTask) error {
			_, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) {
				return current.RevokeWorkerAccess("gen-1")
			})
			return err
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.probeTaskTunnel(context.Background(), "task-1", "gen-1"); !errors.Is(
		err,
		remoteworker.ErrTunnelRevoked,
	) {
		t.Fatalf("revoked task reached tunnel recovery: %v", err)
	}
	if remote.probes.Load() != 1 || processes.starts.Load() != 1 {
		t.Fatal("revoked access caused an external operation")
	}
}

func TestHostRestartServesWorkerSocketBeforeTunnelRecovery(t *testing.T) {
	server, spec, processes, remote := hostTunnelFixture(t)
	for range 2 {
		probed := make(chan error, 16)
		remote.probe = func(ctx context.Context) error {
			connection, err := (&net.Dialer{}).DialContext(
				ctx,
				"unix",
				filepath.Join(server.config.Root, "worker.sock"),
			)
			if err == nil {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(time.Second))
				_, err = io.WriteString(connection, "{}")
				if err == nil {
					err = connection.(*net.UnixConn).CloseWrite()
				}
				if err == nil {
					var response []byte
					response, err = io.ReadAll(connection)
					if err == nil && !strings.Contains(string(response), `"outcome":"invalid"`) {
						err = fmt.Errorf("unexpected worker response: %s", response)
					}
				}
			}
			probed <- err
			return err
		}
		listener, err := server.Listen(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		expected := errors.New("stop after ready")
		err = server.ServeReady(context.Background(), listener.UnixListener, func() error {
			select {
			case err := <-probed:
				if err != nil {
					return fmt.Errorf("worker socket unavailable during restoration: %w", err)
				}
			default:
				return errors.New("restored controller omitted tunnel recovery")
			}
			return expected
		})
		listener.Close()
		if !errors.Is(err, expected) {
			t.Fatal(err)
		}
		config := server.config
		server, err = New(config)
		if err != nil {
			t.Fatal(err)
		}
		server.tunnels, _ = remoteworker.NewTunnelManager(config.Root, processes, remote)
	}
	current, err := server.store.Read(context.Background(), spec.Owner.Task)
	if err != nil || current.Snapshot().Worker.Access.Verifier != spec.Owner.Verifier ||
		processes.starts.Load() != 1 || processes.stops.Load() != 0 {
		t.Fatalf("host restart replaced access or tunnel ownership: %v", err)
	}
}
