package taskops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
)

type restartRuntimeFixture struct {
	retired        []string
	issued         int
	loseActivation bool
	endpointError  error
}

func (runtime *restartRuntimeFixture) CheckEndpoint(context.Context, taskstate.Worker) error {
	return runtime.endpointError
}

func (runtime *restartRuntimeFixture) Prepare(
	_ context.Context,
	task *taskstate.LockedTask,
	generation string,
) (RemoteWorkerSite, error) {
	bound := task.Current().Snapshot().Worker
	if bound.Generation != generation {
		if bound.Access == nil || !bound.Access.Revoked {
			return RemoteWorkerSite{}, errors.New("old transport cleanup preceded grant revocation")
		}
		runtime.retired = append(runtime.retired, bound.Generation)
	}
	tools, err := remoteworker.ToolsForGeneration(
		bound.Lease.Remote,
		task.Current().ID(),
		generation,
	)
	return RemoteWorkerSite{
		Generation: generation,
		Profile: herdr.SavedMachine{
			ID:      bound.Machine,
			Target:  bound.Lease.Remote.Target,
			Session: bound.Session,
			Enabled: true,
		},
		Site: remoteworker.LaunchSite{
			Lease:       bound.Lease,
			Tools:       tools,
			Environment: herdr.PlainWorktree,
		},
	}, err
}

func (runtime *restartRuntimeFixture) Activate(
	_ context.Context,
	task *taskstate.LockedTask,
	site RemoteWorkerSite,
) error {
	bound := task.Current().Snapshot().Worker
	if bound.Generation != site.Generation || bound.PendingLaunch != "" {
		return errors.New("activation preceded native confirmation")
	}
	if bound.Access == nil {
		grant, _, err := workeraccess.Issue(
			task.Current().ID(),
			bound.Generation,
			workeraccess.Operations(),
		)
		if err != nil {
			return err
		}
		if _, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) { return current.InstallWorkerAccess(grant) }); err != nil {
			return err
		}
		runtime.issued++
	}
	if runtime.loseActivation {
		runtime.loseActivation = false
		return errors.New("activation confirmation lost")
	}
	_, err := task.Apply(func(current taskstate.Task) (taskstate.Task, error) {
		return current.ConfirmTransportGeneration(site.Generation)
	})
	return err
}

type restartingRemoteRunner struct {
	base           *initialLaunchRunner
	loseStart      bool
	leaveAbsent    bool
	foreignSession bool
}

func (runner *restartingRemoteRunner) RunMachine(
	ctx context.Context,
	machine string,
	arguments ...string,
) ([]byte, []byte, error) {
	task, err := runner.base.service.store.Read(ctx, "task-1")
	if err != nil {
		return nil, nil, err
	}
	if task.Snapshot().LaunchIntent == nil && runner.base.started && runner.foreignSession &&
		len(arguments) > 1 &&
		arguments[0] == "agent" &&
		arguments[1] == "get" {
		return []byte(
			`{"result":{"agent":{"pane_id":"w1:p2","agent_status":"idle","agent":"codex","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"different-session"}}}}`,
		), nil, nil
	}
	if task.Snapshot().LaunchIntent == nil && len(arguments) > 1 && arguments[0] == "agent" &&
		arguments[1] == "start" {
		bound := task.Snapshot().Worker
		if bound.PendingLaunch != bound.Generation ||
			bound.LaunchAttemptGeneration != bound.Generation ||
			!strings.Contains(strings.Join(arguments, " "), bound.Generation) {
			return nil, nil, errors.New(
				"native restart preceded durable submission or omitted its generation",
			)
		}
		if strings.Contains(strings.Join(arguments, " "), "CONCIERGE_HOME=") ||
			strings.Contains(strings.Join(arguments, " "), "TINCAN_HOME=") {
			return nil, nil, errors.New("native restart received primary filesystem settings")
		}
		runner.base.started = !runner.leaveAbsent
		runner.base.starts++
		if runner.loseStart {
			runner.loseStart = false
			return nil, nil, errors.New("native start confirmation lost")
		}
		return []byte(`{"result":{}}`), nil, nil
	}
	return (initialRemoteHerdr{base: runner.base}).RunMachine(ctx, machine, arguments...)
}

func TestRemoteRestartRecoversWithoutAnotherGenerationAgentOrLease(t *testing.T) {
	for _, mode := range []string{"complete", "lost start", "lost activation", "activation lost agent exits", "unconfirmed absent", "foreign session", "missing probe", "changed endpoint", "reserved partition"} {
		t.Run(mode, func(t *testing.T) {
			service, intent, config, worktrees := remoteInitialLeaseFixture(t)
			base := &initialLaunchRunner{service: service, intent: *intent}
			runner := &restartingRemoteRunner{base: base}
			config.Herdr = herdr.Client{MachineRunner: runner}
			config.Remote.Activate = func(ctx context.Context, taskID, generation string, _ RemoteWorkerTools) error {
				grant, _, err := workeraccess.Issue(taskID, generation, workeraccess.Operations())
				if err != nil {
					return err
				}
				_, err = service.store.Update(
					ctx,
					taskID,
					func(task taskstate.Task) (taskstate.Task, error) {
						task, err := task.InstallWorkerAccess(grant)
						if err != nil {
							return taskstate.Task{}, err
						}
						return task.ConfirmTransportGeneration(generation)
					},
				)
				return err
			}
			ctx := context.Background()
			launched, err := service.ContinueInitialLaunch(ctx, "task-1", intent.Generation, config)
			if err != nil {
				t.Fatal(err)
			}
			original := launched.Snapshot().Worker
			worker, err := service.SupervisedWorker("task-1", config.MessageRoot, config.Herdr)
			if err != nil {
				t.Fatal(err)
			}
			runtime := &restartRuntimeFixture{
				loseActivation: mode == "lost activation" || mode == "activation lost agent exits",
			}
			if mode == "changed endpoint" {
				runtime.endpointError = errors.New("saved endpoint changed")
			}
			probe := &fakeTunnelProbe{task: "task-1", generation: original.Generation}
			worker = worker.WithRemoteRuntime(runtime).WithTunnelProbe(probe)
			if mode == "missing probe" {
				worker = worker.WithTunnelProbe(nil)
			}
			now := time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC)
			worker.now = func() time.Time { return now }
			base.started = false
			runner.loseStart = mode == "lost start" || mode == "unconfirmed absent" ||
				mode == "foreign session"
			runner.leaveAbsent = mode == "unconfirmed absent"
			runner.foreignSession = mode == "foreign session"
			acquisitions := len(worktrees.holders)
			if mode == "reserved partition" {
				if _, err := service.store.Update(ctx, "task-1", func(task taskstate.Task) (taskstate.Task, error) { return task.ReserveLaunch("reserved-next") }); err != nil {
					t.Fatal(err)
				}
				probe.failure = errors.New("SSH tunnel unavailable")
			}
			err = worker.Restart(ctx)
			if mode == "missing probe" || mode == "changed endpoint" ||
				mode == "reserved partition" {
				if err == nil || base.starts != 1 ||
					len(runtime.retired) != 0 ||
					runtime.issued != 0 {
					t.Fatalf("unsupported or unreachable restart reached native effects: %v", err)
				}
				current, err := service.store.Read(ctx, "task-1")
				if err != nil || current.Snapshot().Worker.Generation != original.Generation ||
					current.Snapshot().Worker.Access.Revoked {
					t.Fatal("unsupported restart changed generation or authority")
				}
				return
			}
			if mode == "complete" && err != nil {
				t.Fatal(err)
			}
			if mode != "complete" && !errors.Is(err, supervise.ErrUnreachable) {
				t.Fatalf("uncertain restart did not preserve reachability: %v", err)
			}
			pending, err := service.store.Read(ctx, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			generation := pending.Snapshot().Worker.Generation
			probe.generation = generation
			if connections := pending.Snapshot().Worker.Connections; connections != nil {
				if now.Before(connections.Herdr.NextRetry) {
					now = connections.Herdr.NextRetry
				}
				if now.Before(connections.Tunnel.NextRetry) {
					now = connections.Tunnel.NextRetry
				}
			}
			if mode == "activation lost agent exits" {
				base.started = false
			}
			observation := worker.Observe(ctx)
			if mode == "unconfirmed absent" || mode == "foreign session" {
				current, err := service.store.Read(ctx, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				bound := current.Snapshot().Worker
				if !errors.Is(observation, supervise.ErrUnreachable) ||
					bound.Generation != generation ||
					bound.PendingLaunch != generation ||
					bound.LaunchAttemptGeneration != generation ||
					bound.Lease != original.Lease ||
					base.starts != 2 ||
					runtime.issued != 0 {
					t.Fatalf(
						"unknown native submission was repeated or discarded: %+v %v",
						bound,
						observation,
					)
				}
				return
			}
			if mode == "activation lost agent exits" {
				if !errors.Is(observation, supervise.ErrMissing) ||
					errors.Is(observation, supervise.ErrUnreachable) {
					t.Fatalf("confirmed missing agent blocked transport recovery: %v", observation)
				}
			} else if observation != nil {
				t.Fatal(observation)
			}
			current, err := service.store.Read(ctx, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			bound := current.Snapshot().Worker
			if generation == original.Generation || bound.Generation != generation ||
				bound.PendingLaunch != "" ||
				bound.LaunchAttemptGeneration != "" ||
				bound.TransportGeneration != generation ||
				bound.Lease != original.Lease ||
				runtime.issued != 1 ||
				len(runtime.retired) != 1 ||
				base.starts != 2 ||
				len(worktrees.holders) != acquisitions {
				t.Fatalf(
					"restart changed durable identity: worker=%+v issued=%d retired=%v starts=%d",
					bound,
					runtime.issued,
					runtime.retired,
					base.starts,
				)
			}
			messages, err := worker.messages.List(worker.recipient)
			if err != nil {
				t.Fatal(err)
			}
			assignments := 0
			for _, message := range messages {
				assignment, err := taskprotocol.DecodeAssignment(
					message.Body,
					service.root,
					"task-1",
				)
				if err != nil {
					continue
				}
				if assignment.SpawnGeneration != generation {
					t.Fatal("retained an old assignment instead of its frozen redelivery")
				}
				assignments++
			}
			if assignments != 1 {
				t.Fatalf("redelivered %d assignments", assignments)
			}
		})
	}
}
