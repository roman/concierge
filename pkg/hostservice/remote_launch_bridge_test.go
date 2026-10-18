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
	for _, mode := range []string{"complete", "context confirmation lost", "return confirmation lost", "restart", "restart start lost", "restart activation lost", "result export", "result confirmation lost", "ship", "ship return confirmation lost", "ship agent forwarding", "ship agent forwarding return confirmation lost"} {
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
	}
	configuration := map[string]machineprep.Configuration{"saved-id": {
		Target: "baker", Base: base, Agent: "codex",
		Authentication: authentication,
		Repositories: []machineprep.Repository{
			{Name: "concierge", URL: "ssh://git.test/concierge.git"},
			{Name: "exmachina", URL: "ssh://git.test/project.git"},
		},
	}}
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
		{"clone", "--bare", input.Project, bare},
		{"-C", bare, "update-ref", "refs/remotes/origin/main", input.BaseCommit},
		{"-C", bare, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	} {
		if output, err := exec.Command(server.config.GitExecutable, arguments...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	runner := &launchBridgeSSH{
		server: server,
		manager: remoteworker.Worktrees{
			Git:        worktree.Client{Executable: server.config.GitExecutable},
			CheckQuiet: func(context.Context, string) error { return nil },
		},
	}
	runner.lostContextConfirmation = mode == "context confirmation lost"
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
	policy := "#!/bin/sh\n[ \"$1\" = -G ] || exit 73\nprintf 'hostname synthetic\\nforwardagent no\\nforwardx11 no\\ntunnel false\\n'\n"
	if err := os.WriteFile(filepath.Join(tools, "ssh"), []byte(policy), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	server.config.HerdrExecutable = filepath.Join(tools, "herdr")
	script := fmt.Sprintf(remoteLaunchBridgeHerdr, tools)
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := server.executeInitialLaunch(context.Background(), input, operatorLaunch)
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
		var output bytes.Buffer
		exporter := hostRemoteResultExporter{server: server}
		if mode == "result confirmation lost" {
			runner.lostResultConfirmation = true
			if _, err := exporter.Export(context.Background(), current, "", &output); err == nil ||
				output.Len() != 0 {
				t.Fatal("unconfirmed result reached the primary importer")
			}
		}
		commit, err := exporter.Export(context.Background(), current, "", &output)
		if err != nil {
			t.Fatal(err)
		}
		git := worktree.Client{Executable: server.config.GitExecutable}
		reference := "refs/concierge/results/host-bridge"
		if err := git.ImportResult(context.Background(), input.Project, commit, reference, &output); err != nil {
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
 printf '%%s' '[{"id":"saved-id","label":"baker","target":"baker","session":"default","enabled":true}]'
 exit 0
fi
[ "$1 $2" = '--machine saved-id' ] || exit 73
shift 2
case "$1 $2" in
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
 printf '{"result":{"pane":{"workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","foreground_cwd":"%%s"}}}' "$(cat "$state/cwd")"
 ;;
'pane process-info') printf '%%s' '{"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":100,"foreground_processes":[{"pid":101,"name":"codex","argv0":"codex"}]}}}' ;;
'agent get')
 if [ -f "$state/missing" ] || [ ! -f "$state/started" ]; then printf '%%s' '{"error":{"code":"agent_not_found"}}' >&2; exit 1; fi
 printf '%%s' '{"result":{"agent":{"pane_id":"w1:p1","agent":"codex","agent_status":"idle","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"native-session"}}}}'
 ;;
'agent start')
 rm -f "$state/missing"
 printf 'start\n' >> "$state/started"
 if [ -f "$state/lose-start" ]; then rm "$state/lose-start"; exit 72; fi
 printf '%%s' '{"result":{}}'
 ;;
'pane report-metadata') printf '%%s' '{"result":{}}' ;;
'agent prompt') printf 'prompt\n' >> "$state/prompts"; printf '%%s' '{"result":{}}' ;;
*) exit 73 ;;
esac
`
