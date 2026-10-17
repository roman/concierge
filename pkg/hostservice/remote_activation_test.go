package hostservice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type activationSSH struct {
	server          *Server
	secret          []byte
	installs, calls int
	loseFirstCopy   bool
}

func (runner *activationSSH) Run(
	ctx context.Context,
	args []string,
	input io.Reader,
) ([]byte, []byte, error) {
	runner.calls++
	task, err := runner.server.store.Read(ctx, "task-1")
	if err != nil {
		return nil, nil, err
	}
	access := task.Snapshot().Worker.Access
	if access == nil || access.Revoked {
		return nil, nil, errors.New("secret delivery preceded an active durable grant")
	}
	command := args[len(args)-1]
	if strings.Contains(command, "'worker-secret' 'install'") {
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, nil, err
		}
		if err := access.Authorize("task-1", "gen-1", workeraccess.Receive, data); err != nil {
			return nil, nil, err
		}
		if len(runner.secret) != 0 && !bytes.Equal(runner.secret, data) {
			return nil, nil, errors.New("replaced the original secret")
		}
		runner.secret = bytes.Clone(data)
		runner.installs++
		if runner.loseFirstCopy && runner.installs == 1 {
			return nil, nil, errors.New("copy confirmation lost")
		}
		return []byte("status: installed\n"), nil, nil
	}
	if strings.Contains(command, "'worker-context' 'install'") {
		if access.Authorize("task-1", "gen-1", workeraccess.Receive, runner.secret) != nil {
			return nil, nil, errors.New("remote secret is absent")
		}
		return []byte("status: bound\n"), nil, nil
	}
	return nil, nil, errors.New("unexpected setup SSH command")
}

type remoteActivationFixture struct {
	server    *Server
	profile   herdr.SavedMachine
	request   remoteworker.LaunchSiteRequest
	ssh       *activationSSH
	processes *hostTunnelProcesses
	remote    *hostTunnelRemote
}

func newRemoteActivationFixture(t *testing.T) *remoteActivationFixture {
	t.Helper()
	server := workerHost(t, 3)
	definition := *intakeRequest().Intake
	location := taskstate.RemoteLease{
		Machine:        "machine-1",
		Target:         "synthetic-machine",
		PrimaryRoot:    server.config.Root,
		PrimaryProject: "/primary/project-metadata",
		Base:           "/r",
		Repository:     definition.Repo,
		ClientCommit:   strings.Repeat("b", 40),
	}
	request := remoteworker.WorktreeRequest{
		Task:       "task-1",
		Location:   location,
		Holder:     "stable-holder",
		Branch:     "task/result",
		BaseCommit: strings.Repeat("a", 40),
	}
	location.Backend = taskstate.RemoteGit
	lease := taskstate.WorktreeLease{
		Remote:     location,
		ID:         "lease-one",
		Holder:     request.Holder,
		Project:    location.ProjectPath(),
		Path:       location.PoolRoot() + "/slot/repo",
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
		State:      taskstate.LeaseHeld,
	}
	worker := &taskstate.Worker{
		Name:       "worker",
		Harness:    "codex",
		Machine:    location.Machine,
		Session:    "work",
		Pane:       "pane-1",
		Workspace:  "workspace-1",
		Tab:        "tab-1",
		Project:    lease.Project,
		Worktree:   lease.Path,
		Generation: "gen-1",
		AgentArgs:  []string{"resume", "native-id"},
	}
	intent := &taskstate.LaunchIntent{
		Remote:      request.Location,
		Machine:     location.Machine,
		Lease:       lease,
		LeaseHolder: lease.Holder,
		Generation:  worker.Generation,
		Harness:     worker.Harness,
		Name:        worker.Name,
		Session:     worker.Session,
		Project:     worker.Project,
		Worktree:    worker.Worktree,
		Branch:      lease.Branch,
		BaseCommit:  lease.BaseCommit,
		Workspace:   worker.Workspace,
		Label:       "concierge-gen-1",
		Body:        "Work",
		Phase:       taskstate.LaunchAgentRequested,
		Endpoint: &taskstate.LaunchEndpoint{
			Workspace: worker.Workspace,
			Tab:       worker.Tab,
			Pane:      worker.Pane,
		},
		ResumeArgs: worker.AgentArgs,
	}
	task, err := taskstate.NewMigrated(
		taskstate.Snapshot{
			Definition:   definition,
			State:        taskstate.InFlight,
			Worker:       worker,
			LaunchIntent: intent,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.config.Root, "records", task.ID()+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &remoteActivationFixture{
		server: server,
		profile: herdr.SavedMachine{
			ID:      location.Machine,
			Target:  location.Target,
			Session: worker.Session,
			Enabled: true,
		},
		request: remoteworker.LaunchSiteRequest{
			Worktree:   request,
			Lease:      lease,
			Generation: worker.Generation,
		},
		processes: &hostTunnelProcesses{},
		remote:    &hostTunnelRemote{},
	}
	fixture.ssh = &activationSSH{server: server}
	fixture.connect(t)
	return fixture
}

func (fixture *remoteActivationFixture) connect(t *testing.T) {
	t.Helper()
	fixture.server.preparationSSH = fixture.ssh
	fixture.ssh.server = fixture.server
	var err error
	fixture.server.tunnels, err = remoteworker.NewTunnelManager(
		fixture.server.config.Root,
		fixture.processes,
		fixture.remote,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture *remoteActivationFixture) activate(t *testing.T) error {
	t.Helper()
	tools, err := fixture.request.Tools()
	if err != nil {
		t.Fatal(err)
	}
	return fixture.server.activateRemoteWorker(
		context.Background(),
		"task-1",
		"gen-1",
		fixture.profile,
		tools,
	)
}

func TestHostRemoteActivationRecoversWithoutAnotherSecretOrTunnel(t *testing.T) {
	for _, lost := range []bool{false, true} {
		fixture := newRemoteActivationFixture(t)
		fixture.ssh.loseFirstCopy = lost
		if lost {
			if err := fixture.activate(t); err == nil {
				t.Fatal("confirmed a lost secret-copy response")
			}
			pending, err := fixture.server.store.Read(context.Background(), "task-1")
			if err != nil || pending.Snapshot().Worker.TransportGeneration != "" {
				t.Fatal("unconfirmed setup recorded a ready transport")
			}
		}
		if err := fixture.activate(t); err != nil {
			t.Fatal(err)
		}
		task, err := fixture.server.store.Update(
			context.Background(),
			"task-1",
			func(task taskstate.Task) (taskstate.Task, error) { return task.CompleteInitialLaunch("gen-1") },
		)
		if err != nil {
			t.Fatal(err)
		}
		verifier := task.Snapshot().Worker.Access.Verifier
		reloaded, err := New(fixture.server.config)
		if err != nil {
			t.Fatal(err)
		}
		fixture.server = reloaded
		fixture.connect(t)
		before := fixture.ssh.installs
		if err := fixture.activate(t); err != nil {
			t.Fatal(err)
		}
		task, err = fixture.server.store.Read(context.Background(), "task-1")
		if err != nil {
			t.Fatal(err)
		}
		if task.Snapshot().Worker.Access.Verifier != verifier ||
			task.Snapshot().Worker.TransportGeneration != "gen-1" ||
			fixture.ssh.installs != before ||
			fixture.processes.starts.Load() != 1 {
			t.Fatal("restart replaced the secret or tunnel")
		}
		stage, err := remoteworker.OpenPrimaryCredentialStage(
			context.Background(),
			fixture.server.config.Root,
			fixture.request,
		)
		if err != nil {
			t.Fatal(err)
		}
		data, err := stage.ReadSecret()
		stage.Close()
		if err != nil || len(data) != 0 {
			t.Fatal("retained a completed staging secret")
		}
	}
}
