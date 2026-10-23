package hostservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/worktree"
)

type launchBridgeSSH struct {
	launchFailure           string
	base                    string
	server                  *Server
	manager                 remoteworker.Worktrees
	site                    remoteworker.LaunchSiteRequest
	secretCopies            int
	lostContextConfirmation bool
	lostReturnConfirmation  bool
	lostResultConfirmation  bool
}

func (runner *launchBridgeSSH) RunTo(
	ctx context.Context,
	arguments []string,
	input io.Reader,
	output io.Writer,
) ([]byte, error) {
	if !strings.Contains(arguments[len(arguments)-1], "'worker-worktree' 'export-result'") {
		return nil, errors.New("unexpected streamed bridge operation")
	}
	request, err := remoteworker.DecodeWorktreeResultRequest(input)
	if err != nil {
		return nil, err
	}
	bundle, err := runner.manager.ExportResult(ctx, request)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	if err := json.NewEncoder(output).Encode(remoteworker.WorktreeResult{Lease: request.Lease, Commit: bundle.Commit()}); err != nil {
		return nil, err
	}
	_, err = io.Copy(output, bundle)
	if runner.lostResultConfirmation {
		runner.lostResultConfirmation = false
		return nil, errors.New("result confirmation lost")
	}
	return nil, err
}

func (runner *launchBridgeSSH) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	command := arguments[len(arguments)-1]
	if strings.Contains(command, "concierge-base-resolve") {
		return []byte(runner.base + "\n"), nil, nil
	}
	switch {
	case strings.Contains(command, "concierge-client-status"):
		return []byte(strings.Repeat("a", 40)), nil, nil
	case strings.Contains(command, "'worker-worktree' 'import'"):
		request, bundle, err := remoteworker.DecodeBaseImport(input)
		if err == nil {
			err = runner.manager.ImportBase(ctx, request, bundle)
		}
		return []byte("status: imported"), nil, err
	case strings.Contains(command, "'worker-worktree' 'acquire'"):
		request, err := remoteworker.DecodeWorktreeRequest(input)
		if err != nil {
			return nil, nil, err
		}
		lease, err := runner.manager.Acquire(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(lease)
		return data, nil, err
	case strings.Contains(command, "'worker-worktree' 'prepare-launch'"):
		request, err := remoteworker.DecodeLaunchSiteRequest(input)
		if err != nil {
			return nil, nil, err
		}
		runner.site = request
		switch runner.launchFailure {
		case "transport":
			return nil, []byte("SSH connection closed"), errors.New("exit status 255")
		case "timeout":
			return nil, nil, context.DeadlineExceeded
		case "stderr":
			return nil, []byte("remote warning"), nil
		case "response decoding":
			return []byte("invalid JSON private stdout"), nil, nil
		case "response validation":
			return []byte(`{}`), nil, nil
		case "environment inspection":
			return []byte(
					`{"launch_failure":"environment inspection","diagnostics":"archive HTTP 404"}`,
				), nil, errors.New(
					"exit status 1",
				)
		case "directory ownership":
			return []byte(
					`{"launch_failure":"directory ownership"}`,
				), nil, errors.New(
					"exit status 1",
				)
		}
		site, err := runner.manager.PrepareLaunchSite(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(site)
		return data, nil, err
	case strings.Contains(command, "'worker-secret' 'install'"):
		task, err := runner.server.store.Read(ctx, runner.site.Worktree.Task)
		if err != nil {
			return nil, nil, err
		}
		worker := task.Snapshot().Worker
		if worker == nil || worker.Access == nil {
			return nil, nil, errors.New("secret copied before binding and grant publication")
		}
		secret, err := worker.Access.RestoreSecret(input)
		if err != nil {
			return nil, nil, err
		}
		tools, err := runner.site.Tools()
		if err == nil {
			err = workeraccess.InstallSecretFile(
				tools.TaskDirectory,
				strings.NewReader(string(secret.Bytes())),
			)
		}
		runner.secretCopies++
		return []byte("status: installed"), nil, err
	case strings.Contains(command, "'worker-context' 'install'"):
		if runner.lostContextConfirmation {
			runner.lostContextConfirmation = false
			return nil, nil, errors.New("context confirmation lost")
		}
		return []byte("status: bound"), nil, nil
	case strings.Contains(command, "'worker-secret' 'remove'"):
		task, err := runner.server.store.Read(ctx, runner.site.Worktree.Task)
		if err != nil {
			return nil, nil, err
		}
		worker := task.Snapshot().Worker
		if worker == nil || worker.Access == nil || !worker.Access.Revoked {
			return nil, nil, errors.New("removed a secret before revocation")
		}
		tools, err := runner.site.Tools()
		if err == nil {
			err = workeraccess.RemoveSecretFile(tools.TaskDirectory, worker.Access.Verifier)
		}
		return []byte("status: removed"), nil, err
	case strings.Contains(command, "'worker-worktree' 'return'"):
		request, err := remoteworker.DecodeWorktreeReturnRequest(input)
		if err != nil {
			return nil, nil, err
		}
		lease, err := runner.manager.Return(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		if runner.lostReturnConfirmation {
			runner.lostReturnConfirmation = false
			return nil, nil, errors.New("return confirmation lost")
		}
		data, err := json.Marshal(lease)
		return data, nil, err
	}
	return nil, nil, errors.New("unexpected bridge operation")
}

func TestHostRemoteLaunchConnectsNativeWorktreeActivationAndDispatch(t *testing.T) {
	for _, mode := range []string{"complete", "context confirmation lost", "return confirmation lost", "restart", "restart start lost", "restart activation lost", "result export", "result confirmation lost", "ship", "ship return confirmation lost", "ship agent forwarding", "ship agent forwarding return confirmation lost", "ship oversized result"} {
		t.Run(mode, func(t *testing.T) { testHostRemoteLaunchBridge(t, mode) })
	}
}

func TestFailedRemoteNativeStartupReleasesLaunchAndAllowsAbandonment(t *testing.T) {
	testHostRemoteLaunchBridge(t, "native startup failed")
}

func TestRemoteLaunchStageFailuresRetainGenerationAndLease(t *testing.T) {
	for _, stage := range []string{"transport", "timeout", "stderr", "response decoding",
		"response validation", "environment inspection", "directory ownership"} {
		t.Run(stage, func(t *testing.T) { testHostRemoteLaunchBridge(t, "launch failure "+stage) })
	}
}

func TestAbandonPartialRemoteLaunchReturnsRealWorktree(t *testing.T) {
	for _, mode := range []string{"partial abandon", "partial abandon return confirmation lost", "partial abandon active tunnel", "partial abandon moved pane"} {
		t.Run(mode, func(t *testing.T) { testHostRemoteLaunchBridge(t, mode) })
	}
}

func testHostRemoteLaunchBridge(t *testing.T, mode string) {
	t.Helper()
	server, input, _ := remoteHostLaunchFixture(t)
	server.config.Session, server.config.TreehouseExecutable = "", ""
	if strings.HasPrefix(mode, "ship") {
		configureShipBridge(t, server, input)
	} else if !strings.HasPrefix(mode, "result") {
		if _, err := server.store.Update(context.Background(), input.TaskID, func(task taskstate.Task) (taskstate.Task, error) {
			snapshot := task.Snapshot()
			snapshot.Definition.Kind = "scout"
			return taskstate.NewMigrated(snapshot)
		}); err != nil {
			t.Fatal(err)
		}
	}
	base, err := os.MkdirTemp("/tmp", "host-remote-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "machine-owner"), []byte("saved-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	authentication := sshcommand.Authentication("")
	if strings.Contains(mode, "agent forwarding") {
		authentication = sshcommand.ForwardedAgent
		t.Setenv("SSH_AUTH_SOCK", filepath.Join(base, "primary-agent.sock"))
	}
	configuration := map[string]machineprep.Configuration{"saved-id": {
		Target: "dev-remote", Base: base, Agent: "codex",
		Authentication: authentication,
		Repositories: []machineprep.Repository{
			{Name: "concierge", URL: "ssh://git.test/concierge.git"},
			{Name: "exmachina", URL: "ssh://git.test/project.git"},
		},
	}}
	if mode == "ship oversized result" {
		limited := configuration["saved-id"]
		limited.MaxResultBytes = 1
		configuration["saved-id"] = limited
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.config.Root, "config", "machines.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "repositories"), 0700); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(base, "repositories", "exmachina.git")
	for _, arguments := range [][]string{
		{"-C", input.Project, "config", "core.fsmonitor", "false"},
		{"clone", "--bare", input.Project, bare},
		{"-C", bare, "config", "core.fsmonitor", "false"},
		{"-C", bare, "update-ref", "refs/remotes/origin/main", input.BaseCommit},
		{"-C", bare, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	} {
		if output, err := exec.Command(server.config.GitExecutable, arguments...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	runner := &launchBridgeSSH{
		base:   base,
		server: server,
		manager: remoteworker.Worktrees{
			Git:        worktree.Client{Executable: server.config.GitExecutable},
			CheckQuiet: func(context.Context, string) error { return nil },
		},
	}
	if strings.HasPrefix(mode, "launch failure ") {
		runner.launchFailure = strings.TrimPrefix(mode, "launch failure ")
	}
	runner.lostContextConfirmation = mode == "context confirmation lost" ||
		strings.HasPrefix(mode, "partial abandon")
	runner.lostReturnConfirmation = strings.HasSuffix(mode, "return confirmation lost")
	server.preparationSSH = runner
	processes := &retirementProcesses{}
	server.tunnels, err = remoteworker.NewTunnelManager(
		server.config.Root,
		processes,
		&hostTunnelRemote{},
	)
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	policy := "#!/bin/sh\ncase \"$*\" in *\"'session' 'list'\"*) printf '%s' '{\"sessions\":[{\"name\":\"default\",\"running\":true,\"socket_path\":\"/tmp/bridge.sock\"}]}'; exit 0;; *\"'terminal' 'title' 'clear'\"*) printf '%s' '{\"result\":{\"reason\":\"no_foreground_client\"}}'; exit 0;; esac\n[ \"$1\" = -G ] || exit 73\nprintf 'hostname synthetic\\nforwardagent no\\nforwardx11 no\\ntunnel false\\n'\n"
	if err := os.WriteFile(filepath.Join(tools, "ssh"), []byte(policy), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	server.config.HerdrExecutable = filepath.Join(tools, "herdr")
	script := fmt.Sprintf(remoteLaunchBridgeHerdr, tools)
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if runner.launchFailure != "" {
		var diagnostics bytes.Buffer
		server.config.Diagnostics = &diagnostics
		_, launchErr := server.executeInitialLaunch(t.Context(), input, operatorLaunch)
		if launchErr == nil || !strings.Contains(launchErr.Error(), runner.launchFailure) {
			t.Fatalf("lost launch stage: %v", launchErr)
		}
		current, err := server.store.Read(t.Context(), input.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		intent := current.Snapshot().LaunchIntent
		if intent == nil || intent.Generation == "" ||
			intent.Generation != runner.site.Generation ||
			intent.Lease != runner.site.Lease ||
			intent.Lease.State != taskstate.LeaseHeld {
			t.Fatal("failed preparation changed the captured generation or lease")
		}
		if current.Snapshot().Worker != nil || runner.secretCopies != 0 ||
			processes.starts.Load() != 0 {
			t.Fatal("activated a failed preparation")
		}
		if runner.launchFailure == "environment inspection" &&
			!strings.Contains(diagnostics.String(), "archive HTTP 404") {
			t.Fatalf("host log lost remote stderr: %s", &diagnostics)
		}
		if strings.Contains(diagnostics.String(), "private stdout") {
			t.Fatal("logged an invalid response body")
		}
		return
	}
	if mode == "native startup failed" {
		if err := os.WriteFile(filepath.Join(tools, "fail-native-start"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		response, err := server.launch(ctx, input)
		if err != nil || response.Outcome != Blocked ||
			!strings.Contains(response.Error, "effective permissions reject /remote/task") ||
			ctx.Err() != nil {
			t.Fatalf("native failure: %+v %v", response, err)
		}
		intent := response.Transition.LaunchIntent
		if intent == nil || intent.Phase != taskstate.LaunchAgentRequested ||
			response.Transition.Worker != nil {
			t.Fatal("native failure changed the captured launch")
		}
		release, err := server.controllers.reserveInitialLaunch(
			controllerKey{kind: taskController, id: input.TaskID},
		)
		if err != nil {
			t.Fatalf("failed startup retained its reservation: %v", err)
		}
		release()
		_, err = server.perform(
			ctx,
			Request{
				Operation:  Stop,
				Transition: &TransitionRequest{TaskID: input.TaskID, Generation: intent.Generation},
			},
		)
		if errors.Is(err, errControllerBusy) {
			t.Fatal("stop retained the startup reservation")
		}
		if err == nil || !strings.Contains(err.Error(), "task abandon") {
			t.Fatalf("unbound stop omitted abandonment guidance: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tools, "gone"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		abandoned, err := server.perform(
			ctx,
			Request{
				Operation:  AbandonLaunch,
				Transition: &TransitionRequest{TaskID: input.TaskID, Generation: intent.Generation},
			},
		)
		if err != nil || abandoned.Abandoned == nil || abandoned.Abandoned.Lease != intent.Lease {
			t.Fatalf(
				"failed launch could not be abandoned with its lease retained: %+v %v",
				abandoned,
				err,
			)
		}
		return
	}
	response, err := server.executeInitialLaunch(context.Background(), input, operatorLaunch)
	if strings.HasPrefix(mode, "partial abandon") {
		if err == nil {
			t.Fatal("expected interrupted context installation")
		}
		pending, readErr := server.store.Read(t.Context(), input.TaskID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		intent := pending.Snapshot().LaunchIntent
		if intent == nil || pending.Snapshot().Worker == nil ||
			intent.Lease.State != taskstate.LeaseHeld {
			t.Fatal("partial launch lost its worker or held lease")
		}
		if mode == "partial abandon active tunnel" {
			launchTools, err := runner.site.Tools()
			if err != nil {
				t.Fatal(err)
			}
			if err := server.activateRemoteWorker(t.Context(), input.TaskID, intent.Generation, herdr.SavedMachine{ID: "saved-id", Target: "dev-remote", Session: "default", Enabled: true}, launchTools); err != nil {
				t.Fatal(err)
			}
		}
		request := TransitionRequest{TaskID: input.TaskID, Generation: intent.Generation}
		if _, err := server.abandonLaunch(t.Context(), TransitionRequest{TaskID: input.TaskID, Generation: "wrong-generation"}); !errors.Is(
			err,
			taskstate.ErrInvalidTransition,
		) {
			t.Fatalf("wrong generation: %v", err)
		}
		if mode == "partial abandon moved pane" {
			if err := os.WriteFile(filepath.Join(tools, "moved"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := server.abandonLaunch(t.Context(), request); err == nil {
				t.Fatal("closed a moved pane")
			}
			current, err := server.store.Read(t.Context(), input.TaskID)
			if err != nil || current.Snapshot().Worker == nil ||
				current.Snapshot().LaunchIntent == nil ||
				current.Snapshot().LaunchIntent.Lease != intent.Lease {
				t.Fatal("uncertain pane lost the retirement binding")
			}
			if _, err := os.Stat(filepath.Join(tools, "gone")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("closed a moved pane")
			}
			if err := os.Remove(filepath.Join(tools, "moved")); err != nil {
				t.Fatal(err)
			}
		}
		abandoned, err := server.abandonLaunch(t.Context(), request)
		if strings.HasSuffix(mode, "return confirmation lost") {
			if !errors.Is(err, remoteworker.ErrReturnTransport) ||
				errors.Is(err, remoteworker.ErrWorktreeOwnership) {
				t.Fatalf("lost return: %v", err)
			}
			abandoned, err = server.abandonLaunch(t.Context(), request)
		}
		if err != nil {
			t.Fatalf("retire partial launch: %v", err)
		}
		if abandoned.Transition.Worker != nil || abandoned.Transition.LaunchIntent != nil ||
			abandoned.Transition.State != taskstate.Queued ||
			abandoned.Abandoned.Lease.State != taskstate.LeaseReturned {
			t.Fatalf("incomplete partial retirement: %+v", abandoned)
		}
		lease, err := runner.manager.Return(
			t.Context(),
			remoteworker.WorktreeReturnRequest{
				Worktree: runner.site.Worktree,
				Lease:    intent.Lease,
				Proof: remoteworker.WorktreeReturnProof{
					Outcome: remoteworker.WorktreeAbandoned,
					Commit:  intent.BaseCommit,
				},
			},
		)
		if err != nil || lease.State != taskstate.LeaseReturned || lease.ID != intent.Lease.ID {
			t.Fatalf("remote lease: %+v %v", lease, err)
		}
		launchTools, _ := runner.site.Tools()
		if _, err := os.Stat(filepath.Join(launchTools.TaskDirectory, "secret")); !errors.Is(
			err,
			os.ErrNotExist,
		) {
			t.Fatalf("retained secret: %v", err)
		}
		if _, err := os.Stat(intent.Lease.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retained Git checkout: %v", err)
		}
		if data, err := exec.Command(server.config.GitExecutable, "-C", intent.Project, "rev-parse", "refs/heads/"+intent.Branch).Output(); err != nil ||
			strings.TrimSpace(string(data)) != intent.BaseCommit {
			t.Fatalf("lost retained branch: %s %v", data, err)
		}
		if processes.starts.Load() != processes.stops.Load() {
			t.Fatal("retained active tunnel")
		}
		if _, err := os.Stat(filepath.Join(tools, "gone")); err != nil {
			t.Fatal("pane was not closed")
		}
		if _, err := server.abandonLaunch(t.Context(), request); err != nil {
			t.Fatalf("retirement retry: %v", err)
		}
		return
	}
	expectedCopies := 1
	verifier := ""
	if mode == "context confirmation lost" {
		if err == nil {
			t.Fatal("lost context confirmation completed the launch")
		}
		pending, readErr := server.store.Read(context.Background(), input.TaskID)
		if readErr != nil || pending.Snapshot().Worker == nil ||
			pending.Snapshot().Worker.Access == nil {
			t.Fatalf("lost setup did not retain the grant: %v", readErr)
		}
		verifier = pending.Snapshot().Worker.Access.Verifier
		response, err = server.executeInitialLaunch(context.Background(), input, recoveryLaunch)
		expectedCopies = 2
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLaunchResponse(response, Request{Operation: Launch, Launch: &input}); err != nil {
		t.Fatal(err)
	}
	worker := response.Transition.Worker
	if worker.Lease.Remote.Authentication != authentication {
		t.Fatal("native ship lease lost the selected authentication mode")
	}
	if worker.Access == nil || worker.Access.Revoked ||
		worker.Lease.Remote.Machine != input.Machine ||
		runner.secretCopies != expectedCopies ||
		processes.starts.Load() != 1 ||
		(verifier != "" && worker.Access.Verifier != verifier) {
		t.Fatalf(
			"incomplete activation: worker=%+v copies=%d tunnels=%d",
			worker,
			runner.secretCopies,
			processes.starts.Load(),
		)
	}
	toolsForWorker, err := runner.site.Tools()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := workeraccess.ReadSecretFile(
		filepath.Join(toolsForWorker.TaskDirectory, "secret"),
	)
	if err != nil ||
		worker.Access.Authorize(
			input.TaskID,
			worker.Generation,
			workeraccess.Receive,
			secret,
		) != nil {
		t.Fatal("native secret does not authorize the dispatched generation")
	}
	if data, err := os.ReadFile(filepath.Join(tools, "prompts")); err != nil ||
		string(data) != "prompt\n" {
		t.Fatalf("assignment dispatch: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(tools, "started")); err != nil ||
		string(data) != "start\n" {
		t.Fatalf("agent startup: %q %v", data, err)
	}
	pendingReply := filepath.Join(toolsForWorker.TaskDirectory, "unsent-result")
	if err := os.WriteFile(pendingReply, []byte("retain this reply"), 0600); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(mode, "result") {
		for _, arguments := range [][]string{
			{"-C", worker.Worktree, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "Remote ship result"},
		} {
			if data, err := exec.Command(server.config.GitExecutable, arguments...).CombinedOutput(); err != nil {
				t.Fatalf("remote result commit: %s %v", data, err)
			}
		}
		current, err := server.store.Read(context.Background(), input.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		exporter := hostRemoteResultExporter{server: server}
		if mode == "result confirmation lost" {
			runner.lostResultConfirmation = true
			if bundle, err := exporter.Export(context.Background(), current, ""); err == nil ||
				bundle != nil {
				t.Fatal("unconfirmed result reached the primary importer")
			}
		}
		bundle, err := exporter.Export(context.Background(), current, "")
		if err != nil {
			t.Fatal(err)
		}
		defer bundle.Close()
		commit := bundle.Commit()
		git := worktree.Client{Executable: server.config.GitExecutable}
		reference := "refs/concierge/results/host-bridge"
		if err := git.ImportResult(context.Background(), input.Project, commit, reference, bundle); err != nil {
			t.Fatal(err)
		}
		if err := git.VerifyResult(context.Background(), input.Project, commit, reference); err != nil {
			t.Fatal(err)
		}
		if lease, err := runner.manager.Inspect(context.Background(), runner.site.Worktree); err != nil ||
			lease != worker.Lease {
			t.Fatal("result export changed the remote lease")
		}
		return
	}
	if strings.HasPrefix(mode, "ship") {
		commitShipBridgeResult(t, server, *worker)
	}
	if mode == "ship oversized result" {
		directory := t.TempDir()
		t.Setenv("TMPDIR", directory)
		if _, err := server.service.MaterializeRemoteResult(t.Context(), input.TaskID, worker.Generation); !errors.Is(
			err,
			sshcommand.ErrOutputLimit,
		) {
			t.Fatalf("oversized result accepted: %v", err)
		}
		current, err := server.store.Read(t.Context(), input.TaskID)
		if err != nil || current.Snapshot().Worker.RemoteResult != (taskstate.RemoteResult{}) {
			t.Fatalf("oversized result selected: %+v %v", current.Snapshot().Worker, err)
		}
		refs, err := exec.Command(server.config.GitExecutable, "-C", input.Project, "for-each-ref", "refs/concierge/results/").
			CombinedOutput()
		if err != nil || len(refs) != 0 {
			t.Fatalf("oversized result imported: %s %v", refs, err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatalf("overflow retained spool: %v %v", entries, err)
		}
		return
	}
	expectedTunnels := int32(1)
	if strings.HasPrefix(mode, "restart") {
		oldGeneration := worker.Generation
		oldDirectory := toolsForWorker.TaskDirectory
		if err := os.WriteFile(filepath.Join(tools, "missing"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "restart start lost" {
			if err := os.WriteFile(filepath.Join(tools, "lose-start"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		runner.lostContextConfirmation = mode == "restart activation lost"
		supervised, err := server.service.SupervisedWorker(
			input.TaskID,
			server.config.MessageRoot,
			herdr.NewClient(server.config.HerdrExecutable),
		)
		if err != nil {
			t.Fatal(err)
		}
		supervised = supervised.WithRemoteRuntime(hostRemoteRuntime{server: server}).
			WithTunnelProbe(taskTunnelProbe{server: server})
		restartContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		restartErr := supervised.Restart(restartContext)
		if mode == "restart" && restartErr != nil {
			t.Fatal(restartErr)
		}
		if mode != "restart" && restartErr == nil {
			t.Fatal("lost restart confirmation completed without recovery")
		}
		pending, err := server.store.Read(restartContext, input.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if connections := pending.Snapshot().Worker.Connections; connections != nil {
			retry := connections.Herdr.NextRetry
			if retry.Before(connections.Tunnel.NextRetry) {
				retry = connections.Tunnel.NextRetry
			}
			if delay := time.Until(retry); delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-restartContext.Done():
					t.Fatal(restartContext.Err())
				}
			}
		}
		if err := supervised.Observe(restartContext); err != nil {
			t.Fatal(err)
		}
		current, err := server.store.Read(restartContext, input.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		worker = current.Snapshot().Worker
		if worker.Generation == oldGeneration || worker.TransportGeneration != worker.Generation ||
			worker.PendingLaunch != "" ||
			worker.LaunchAttemptGeneration != "" {
			t.Fatal("restart did not confirm one new generation")
		}
		if _, err := os.Lstat(filepath.Join(oldDirectory, "secret")); !errors.Is(
			err,
			os.ErrNotExist,
		) {
			t.Fatal("restart retained the revoked secret")
		}
		toolsForWorker, err = runner.site.Tools()
		if err != nil {
			t.Fatal(err)
		}
		secret, err := workeraccess.ReadSecretFile(
			filepath.Join(toolsForWorker.TaskDirectory, "secret"),
		)
		if err != nil ||
			worker.Access.Authorize(
				input.TaskID,
				worker.Generation,
				workeraccess.Receive,
				secret,
			) != nil {
			t.Fatal("new generation did not receive its own secret")
		}
		if data, err := os.ReadFile(filepath.Join(tools, "started")); err != nil ||
			string(data) != "start\nstart\n" {
			t.Fatalf("restart duplicated the native agent: %q %v", data, err)
		}
		expectedTunnels = 2
	}
	if _, err := server.service.Finish(context.Background(), input.TaskID, worker.Generation, taskstate.Close, "2026-10-17"); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(mode, "ship") {
		landShipBridgeResult(t, server, input, *worker)
	}
	if err := os.WriteFile(filepath.Join(tools, "gone"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	cleanup := Request{
		Operation:  Cleanup,
		Transition: &TransitionRequest{TaskID: input.TaskID, Generation: worker.Generation},
	}
	retired, err := server.execute(context.Background(), cleanup, nil)
	if strings.HasSuffix(mode, "return confirmation lost") {
		if err == nil {
			t.Fatal("lost native return confirmation completed primary cleanup")
		}
		pending, readErr := server.store.Read(context.Background(), input.TaskID)
		if readErr != nil || pending.Snapshot().Worker == nil ||
			pending.Snapshot().Completion == nil ||
			!pending.Snapshot().Worker.Access.Revoked ||
			pending.Snapshot().Worker.Lease.State != taskstate.LeaseHeld {
			t.Fatal("uncertain return lost the primary's cleanup binding")
		}
		if strings.HasPrefix(mode, "ship") &&
			pending.Snapshot().Worker.RemoteResult.Lease.State != taskstate.LeaseHeld {
			t.Fatal("uncertain native return released the primary result lease")
		}
		retired, err = server.execute(context.Background(), cleanup, nil)
	}
	if err != nil || retired.Snapshot().Worker != nil || retired.Snapshot().Completion != nil ||
		retired.Snapshot().Receipt == nil ||
		retired.Snapshot().Receipt.Lease.State != taskstate.LeaseReturned {
		t.Fatalf("native cleanup: %+v %v", retired.Snapshot(), err)
	}
	if strings.HasPrefix(mode, "ship") {
		result, found := retired.RetiredRemoteResult(worker.Generation)
		if !found || result.Lease.State != taskstate.LeaseReturned {
			t.Fatal("ship cleanup retained the primary result lease")
		}
	}
	if _, err := os.Lstat(worker.Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native physical checkout remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(toolsForWorker.TaskDirectory, "secret")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("native secret remains: %v", err)
	}
	if data, err := os.ReadFile(pendingReply); err != nil || string(data) != "retain this reply" {
		t.Fatal("cleanup discarded an unsent result")
	}
	if processes.stops.Load() != expectedTunnels || processes.starts.Load() != expectedTunnels {
		t.Fatal("cleanup did not stop exactly the owned tunnel")
	}
}

const remoteLaunchBridgeHerdr = `#!/bin/sh
state='%s'
if [ "$1 $2" = 'machine list' ]; then
 printf '%%s' '[{"id":"saved-id","label":"dev-remote","target":"dev-remote","session":"default","enabled":true}]'
 exit 0
fi
[ "$1 $2" = '--machine saved-id' ] || exit 73
shift 2
case "$1 $2" in
'workspace list') if [ -f "$state/gone" ]; then printf '%%s' '{"result":{"workspaces":[]}}'; else printf '%%s' '{"result":{"workspaces":[{"workspace_id":"w1","active_tab_id":"w1:t1","focused":true}]}}'; fi ;;
'tab list') printf '%%s' '{"result":{"tabs":[{"tab_id":"w1:t1","focused":true}]}}' ;;
'pane close') touch "$state/gone"; printf '%%s' '{"result":{}}' ;;
'workspace create')
 shift 2
 while [ "$#" -gt 0 ]; do
  case "$1" in --cwd) printf '%%s' "$2" > "$state/cwd"; shift ;; --label) printf '%%s' "$2" > "$state/label"; shift ;; esac
  shift
 done
 printf '{"result":{"workspace":{"workspace_id":"w1","label":"%%s"},"tab":{"workspace_id":"w1","tab_id":"w1:t1"},"root_pane":{"pane_id":"w1:p1"}}}' "$(cat "$state/label")"
 ;;
'pane get')
 if [ -f "$state/gone" ]; then printf '%%s' '{"error":{"code":"pane_not_found"}}' >&2; exit 1; fi
 if [ -f "$state/moved" ]; then printf '%%s' '{"result":{"pane":{"workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p1"}}}'; exit 0; fi
 printf '{"result":{"pane":{"workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","foreground_cwd":"%%s"}}}' "$(cat "$state/cwd")"
 ;;
'pane process-info')
 if [ -f "$state/fail-native-start" ]; then printf '%%s' '{"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":100,"foreground_processes":[{"pid":100,"name":"zsh","argv0":"zsh"}]}}}';
 else printf '%%s' '{"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":100,"foreground_processes":[{"pid":101,"name":"codex","argv0":"codex"}]}}}'; fi ;;
'agent read')
 if [ -f "$state/fail-native-start" ] && [ -f "$state/started" ]; then printf 'Error adding directories: effective permissions reject /remote/task\nshell prompt\n'; else printf 'shell prompt\n'; fi ;;
'agent get')
 if [ -f "$state/gone" ] || [ -f "$state/fail-native-start" ] || [ -f "$state/missing" ] || [ ! -f "$state/started" ]; then printf '%%s' '{"error":{"code":"agent_not_found"}}' >&2; exit 1; fi
 printf '%%s' '{"result":{"agent":{"pane_id":"w1:p1","agent":"codex","agent_status":"idle","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"native-session"}}}}'
 ;;
'agent start')
 rm -f "$state/missing"
 printf 'start\n' >> "$state/started"
 if [ -f "$state/fail-native-start" ]; then exec sleep 60; fi
 if [ -f "$state/lose-start" ]; then rm "$state/lose-start"; exit 72; fi
 printf '%%s' '{"result":{}}'
 ;;
'pane report-metadata') printf '%%s' '{"result":{}}' ;;
'agent prompt') printf 'prompt\n' >> "$state/prompts"; printf '%%s' '{"result":{}}' ;;
*) exit 73 ;;
esac
`
