package taskops

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type initialRemoteHerdr struct{ base *initialLaunchRunner }

func (remote initialRemoteHerdr) RunMachine(
	ctx context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	task, err := remote.base.service.store.Read(ctx, "task-1")
	if err != nil {
		return nil, nil, err
	}
	if intent := task.Snapshot().LaunchIntent; intent != nil {
		remote.base.intent = *intent
	}
	if machine != remote.base.intent.Machine {
		return nil, nil, errors.New("wrong machine")
	}
	if len(args) > 1 && args[0] == "tab" && args[1] == "create" {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "CONCIERGE_HOME=") || strings.Contains(joined, "TINCAN_HOME=") {
			return nil, nil, errors.New("primary filesystem environment reached the remote pane")
		}
	}
	return remote.base.RunSession(ctx, remote.base.intent.Session, args...)
}

func TestRemoteInitialLaunchRequiresTransportActivationBeforeEffects(t *testing.T) {
	service, intent, config, remote := remoteInitialLeaseFixture(t)
	if _, err := service.ContinueInitialLaunch(context.Background(), "task-1", intent.Generation, config); !errors.Is(
		err,
		ErrRemoteLaunchUnavailable,
	) {
		t.Fatalf("missing activation callback: %v", err)
	}
	if remote.imports != 0 || len(remote.holders) != 0 {
		t.Fatal("prepared resources without transport activation support")
	}
}

func TestRemoteInitialLaunchCannotDispatchWithoutAnInstalledGrant(t *testing.T) {
	service, intent, config, _ := remoteInitialLeaseFixture(t)
	runner := &initialLaunchRunner{service: service, intent: *intent}
	config.Herdr = herdr.Client{MachineRunner: initialRemoteHerdr{base: runner}}
	config.Remote.Activate = func(context.Context, string, string, RemoteWorkerTools) error { return nil }
	if _, err := service.ContinueInitialLaunch(context.Background(), "task-1", intent.Generation, config); err == nil {
		t.Fatal("dispatched without a durable active grant")
	}
	if _, err := os.Stat(config.MessageRoot); !os.IsNotExist(err) {
		t.Fatal("published work without worker authorization")
	}
}

func TestRemoteInitialLaunchBindsAndActivatesBeforeDispatch(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		service, intent, config, _ := remoteInitialLeaseFixture(t)
		runner := &initialLaunchRunner{service: service, intent: *intent}
		config.Herdr = herdr.Client{MachineRunner: initialRemoteHerdr{base: runner}}
		activations := 0
		verifier := ""
		config.Remote.Activate = func(ctx context.Context, taskID, generation string, tools RemoteWorkerTools) error {
			activations++
			task, err := service.store.Read(ctx, taskID)
			if err != nil {
				return err
			}
			worker := task.Snapshot().Worker
			if task.State() != taskstate.InFlight || worker == nil ||
				worker.Generation != generation ||
				worker.PendingAdoption {
				t.Fatal("activated transport before a stable worker binding")
			}
			expected, err := remoteworker.ToolsForGeneration(intent.Remote, taskID, generation)
			if err != nil || tools != expected {
				t.Fatal("changed the native transport paths")
			}
			if worker.Access == nil {
				grant, _, err := workeraccess.Issue(taskID, generation, workeraccess.Operations())
				if err != nil {
					return err
				}
				verifier = grant.Verifier
				if _, err := service.store.Update(ctx, taskID, func(task taskstate.Task) (taskstate.Task, error) { return task.InstallWorkerAccess(grant) }); err != nil {
					return err
				}
			} else if worker.Access.Verifier != verifier {
				t.Fatal("changed a verifier during launch recovery")
			}
			if interrupt && activations == 1 {
				return errors.New("setup confirmation lost")
			}
			return nil
		}
		ctx := context.Background()
		if interrupt {
			if _, err := service.ContinueInitialLaunch(ctx, "task-1", intent.Generation, config); err == nil {
				t.Fatal("confirmed interrupted transport setup")
			}
			if _, err := os.Stat(config.MessageRoot); !os.IsNotExist(err) {
				t.Fatal("dispatched before transport activation")
			}
		}
		task, err := service.ContinueInitialLaunch(ctx, "task-1", intent.Generation, config)
		if err != nil {
			t.Fatal(err)
		}
		if task.Snapshot().LaunchIntent != nil ||
			task.Snapshot().Worker.Lease.Remote.Machine != intent.Machine ||
			task.Snapshot().Worker.Access.Verifier != verifier {
			t.Fatal("lost remote launch identity or access")
		}
		if runner.creates != 1 || runner.starts != 1 {
			t.Fatal("created a second pane or native agent")
		}
		joined := strings.Join(runner.startArgs, " ")
		if strings.Contains(joined, service.root) || strings.Contains(joined, "/tools/") {
			t.Fatal("sent primary paths in the native worker prompt")
		}
		if !strings.Contains(joined, "CONCIERGE_WORKER_SECRET_FILE") ||
			!strings.Contains(joined, "--add-dir") {
			t.Fatal("lost the remote native context")
		}
	}
}
