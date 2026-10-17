package hostservice

import (
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

	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/worktree"
)

type launchBridgeSSH struct {
	server                  *Server
	manager                 remoteworker.Worktrees
	site                    remoteworker.LaunchSiteRequest
	secretCopies            int
	lostContextConfirmation bool
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
	}
	return nil, nil, errors.New("unexpected bridge operation")
}

func TestHostRemoteLaunchConnectsNativeWorktreeActivationAndDispatch(t *testing.T) {
	for _, mode := range []string{"complete", "context confirmation lost"} {
		t.Run(mode, func(t *testing.T) { testHostRemoteLaunchBridge(t, mode) })
	}
}

func testHostRemoteLaunchBridge(t *testing.T, mode string) {
	t.Helper()
	server, input, _ := remoteHostLaunchFixture(t)
	server.config.Session, server.config.TreehouseExecutable = "", ""
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
	configuration := map[string]machineprep.Configuration{"saved-id": {
		Target: "baker", Base: base, Agent: "codex",
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
			Git: worktree.Client{Executable: server.config.GitExecutable},
		},
	}
	runner.lostContextConfirmation = mode == "context confirmation lost"
	server.preparationSSH = runner
	processes := &hostTunnelProcesses{}
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
'pane get') printf '{"result":{"pane":{"workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","foreground_cwd":"%%s"}}}' "$(cat "$state/cwd")" ;;
'pane process-info') printf '%%s' '{"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":100,"foreground_processes":[{"pid":101,"name":"codex","argv0":"codex"}]}}}' ;;
'agent get')
 if [ ! -f "$state/started" ]; then printf '%%s' '{"error":{"code":"agent_not_found"}}' >&2; exit 1; fi
 printf '%%s' '{"result":{"agent":{"pane_id":"w1:p1","agent":"codex","agent_status":"idle","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"native-session"}}}}'
 ;;
'agent start') printf 'start\n' >> "$state/started"; printf '%%s' '{"result":{}}' ;;
'pane report-metadata') printf '%%s' '{"result":{}}' ;;
'agent prompt') printf 'prompt\n' >> "$state/prompts"; printf '%%s' '{"result":{}}' ;;
*) exit 73 ;;
esac
`
