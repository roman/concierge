package hostservice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

type interruptedLaunchSSH struct {
	clientReads int
	imports     []remoteworker.WorktreeRequest
}

func (runner *interruptedLaunchSSH) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	command := arguments[len(arguments)-1]
	if strings.Contains(command, "concierge-base-resolve") {
		return []byte("/remote/base\n"), nil, nil
	}
	if strings.Contains(command, "concierge-client-status") {
		runner.clientReads++
		return []byte(strings.Repeat("a", 40)), nil, nil
	}
	if strings.Contains(command, "'worker-worktree' 'import'") {
		request, bundle, err := remoteworker.DecodeBaseImport(input)
		if err != nil {
			return nil, nil, err
		}
		if _, err := io.Copy(io.Discard, bundle); err != nil {
			return nil, nil, err
		}
		runner.imports = append(runner.imports, request)
		return nil, nil, errors.New("import confirmation lost")
	}
	return nil, nil, errors.New("unexpected launch SSH operation")
}

func remoteHostLaunchFixture(t *testing.T) (*Server, LaunchRequest, *interruptedLaunchSSH) {
	t.Helper()
	server := workerHost(t, 3)
	server.controllers = newControllerRegistry(context.Background())
	t.Cleanup(func() { _ = server.controllers.close() })
	server.preparationMachines = preparationCatalog{}
	runner := &interruptedLaunchSSH{}
	server.preparationSSH = runner
	definition := *intakeRequest().Intake
	task, err := taskstate.NewQueued(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(server.config.Root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	configuration := `{"saved-id":{"target":"dev-remote","base":"/remote/base","agent":"codex","repositories":[{"name":"concierge","url":"ssh://git.test/concierge.git"},{"name":"` + definition.Repo + `","url":"ssh://git.test/project.git"}]}}`
	if err := os.WriteFile(filepath.Join(server.config.Root, "config", "machines.json"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	server.config.GitExecutable = git
	run := func(arguments ...string) string {
		t.Helper()
		output, err := exec.Command(git, append([]string{"-C", project}, arguments...)...).
			CombinedOutput()
		if err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-b", "main")
	run("remote", "add", "origin", "ssh://git.test/project.git")
	run(
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"--allow-empty",
		"-m",
		"Base",
	)
	return server, LaunchRequest{
		TaskID: definition.ID, Machine: "saved-id", Harness: "codex", Project: project,
		AgentArgs: []string{"--sandbox=workspace-write"},
		Branch:    "task/result", BaseCommit: run("rev-parse", "HEAD"), Body: "Complete the task.",
	}, runner
}

func TestRemoteDefaultPermissionsFailBeforePreparation(t *testing.T) {
	server, input, runner := remoteHostLaunchFixture(t)
	input.AgentArgs = nil
	_, err := server.executeInitialLaunch(t.Context(), input, operatorLaunch)
	if err == nil || !strings.Contains(err.Error(), "--agent-arg=--sandbox=workspace-write") {
		t.Fatalf("missing early permission guidance: %v", err)
	}
	task, readErr := server.store.Read(t.Context(), input.TaskID)
	if readErr != nil || task.Snapshot().LaunchIntent != nil || runner.clientReads != 0 ||
		len(runner.imports) != 0 {
		t.Fatalf("default permissions caused launch effects: %+v %v", task, readErr)
	}
}

func TestRemotePermissionsRequireAnExplicitCompleteMode(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		accepted  bool
	}{
		{"default", nil, false},
		{"long", []string{"--sandbox=workspace-write"}, true},
		{"separate", []string{"--sandbox", "workspace-write"}, true},
		{"short", []string{"-s", "workspace-write"}, true},
		{"read-only", []string{"--sandbox=read-only"}, false},
		{"last mode", []string{"--sandbox=workspace-write", "--sandbox=read-only"}, false},
		{"incomplete", []string{"--sandbox=workspace-write", "--sandbox"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateRemotePermissions(
				LaunchRequest{Harness: "codex", AgentArgs: test.arguments},
			)
			if (err == nil) != test.accepted {
				t.Fatalf("permission validation: %v", err)
			}
		})
	}
	if err := validateRemotePermissions(LaunchRequest{Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
}

func TestHostRemoteLaunchCapturesDestinationBeforeTransferAndRecoversIdentity(t *testing.T) {
	server, input, runner := remoteHostLaunchFixture(t)
	// Remote launch needs neither the primary's session nor its Treehouse.
	server.config.Session, server.config.TreehouseExecutable = "", ""
	ctx := context.Background()
	if _, err := server.executeInitialLaunch(ctx, input, operatorLaunch); !errors.Is(
		err,
		remoteworker.ErrWorktreeOwnership,
	) {
		t.Fatalf("remote import failure: %v", err)
	}
	task, err := server.store.Read(ctx, input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	intent := task.Snapshot().LaunchIntent
	if intent == nil || intent.Machine != "saved-id" || intent.Session != "default" ||
		intent.Remote.PrimaryProject != input.Project ||
		intent.Project != intent.Remote.ProjectPath() ||
		intent.LeaseHolder == "" ||
		intent.Phase != taskstate.LaunchLeaseRequested ||
		len(runner.imports) != 1 {
		t.Fatalf("uncaptured destination: %+v imports=%+v", intent, runner.imports)
	}
	restarted, err := New(server.config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.controllers = newControllerRegistry(ctx)
	t.Cleanup(func() { _ = restarted.controllers.close() })
	restarted.preparationMachines, restarted.preparationSSH = server.preparationMachines, runner
	key := controllerKey{kind: taskController, id: input.TaskID}
	if err := restarted.controllers.awaitInitialWakeup(key); err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileInitialTask(ctx, key); !errors.Is(
		err,
		remoteworker.ErrWorktreeOwnership,
	) {
		t.Fatalf("recovery import failure: %v", err)
	}
	after, err := server.store.Read(ctx, input.TaskID)
	if err != nil || !reflect.DeepEqual(after.Snapshot(), task.Snapshot()) ||
		runner.clientReads != 1 ||
		len(runner.imports) != 2 ||
		runner.imports[0] != runner.imports[1] {
		t.Fatalf(
			"recovery changed identity: %+v reads=%d imports=%+v err=%v",
			after.Snapshot(),
			runner.clientReads,
			runner.imports,
			err,
		)
	}
	input.Branch = "different"
	if _, err := server.executeInitialLaunch(ctx, input, operatorLaunch); err == nil ||
		len(runner.imports) != 2 {
		t.Fatalf("changed retry submitted external work: %v", err)
	}
}
