package taskops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

type reopenRunner struct {
	*workerRunner
	missingPane  bool
	creationLost bool
	creates      int
	label        string
}

func (runner *reopenRunner) RunSession(
	ctx context.Context,
	session string,
	args ...string,
) ([]byte, []byte, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace create":
		runner.creates++
		for index, argument := range args {
			if argument == "--label" {
				runner.label = args[index+1]
			}
		}
		if runner.creationLost {
			return nil, nil, errors.New("lost creation confirmation")
		}
		return []byte(
			fmt.Sprintf(
				`{"result":{"workspace":{"workspace_id":"reopen-workspace","label":%q},"tab":{"workspace_id":"reopen-workspace","tab_id":"reopen-tab"},"root_pane":{"pane_id":"reopen-pane"}}}`,
				runner.label,
			),
		), nil, nil
	case "workspace list":
		return []byte(
			fmt.Sprintf(
				`{"result":{"workspaces":[{"workspace_id":"reopen-workspace","label":%q}]}}`,
				runner.label,
			),
		), nil, nil
	case "pane list":
		return []byte(
			`{"result":{"panes":[{"workspace_id":"reopen-workspace","tab_id":"reopen-tab","pane_id":"reopen-pane"}]}}`,
		), nil, nil
	case "pane get":
		if args[2] == "pane-1" && runner.missingPane {
			return []byte(`{"error":{"code":"pane_not_found"}}`), nil, errors.New("exit 1")
		}
		if args[2] == "reopen-pane" {
			return []byte(
				fmt.Sprintf(
					`{"result":{"pane":{"pane_id":"reopen-pane","workspace_id":"reopen-workspace","tab_id":"reopen-tab","foreground_cwd":%q}}}`,
					runner.worktree,
				),
			), nil, nil
		}
	}
	return runner.workerRunner.RunSession(ctx, session, args...)
}

func TestReopenMissingPane(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost confirmation=%t", lost), func(t *testing.T) {
			ctx := context.Background()
			service, base, messages := runningWorkerService(t)
			makeScout(t, service)
			if _, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Close, "2026-10-20"); err != nil {
				t.Fatal(err)
			}
			base.absent = true
			runner := &reopenRunner{workerRunner: base, missingPane: true, creationLost: lost}
			client := herdr.Client{SessionRunner: runner}
			_, err := service.Reopen(ctx, "task-1", client)
			if lost {
				if err == nil || !strings.Contains(err.Error(), "lost creation confirmation") {
					t.Fatalf("creation: %v", err)
				}
				_, err = service.Reopen(ctx, "task-1", client)
			}
			if err != nil {
				t.Fatal(err)
			}
			current, _ := service.store.Read(ctx, "task-1")
			if current.Snapshot().Worker.Pane != "reopen-pane" || runner.creates != 1 {
				t.Fatalf(
					"pane recovery duplicated creation: %+v, creates %d",
					current.Snapshot(),
					runner.creates,
				)
			}
			worker, err := service.SupervisedWorker("task-1", messages, client)
			if err != nil {
				t.Fatal(err)
			}
			runner.pane = "reopen-pane"
			if err := worker.Restart(ctx); err != nil {
				t.Fatal(err)
			}
			current, _ = service.store.Read(ctx, "task-1")
			if current.Snapshot().Worker.Generation == "gen-1" ||
				!strings.Contains(strings.Join(runner.startArgs, " "), "resume thread-1") {
				t.Fatal("resume lost recorded identity or generation advancement")
			}
		})
	}
}

func TestReopenStates(t *testing.T) {
	for _, trial := range []struct{ name, wantError string }{
		{"accepted live worker", ""},
		{"closed missing agent", ""},
		{"identity missing", "native session identity"},
		{"landed project task", "held worktree lease"},
		{"retired worker", "worker binding"},
		{"unreadable endpoint", "worker liveness"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			ctx := context.Background()
			service, runner, messages := runningWorkerService(t)
			makeScout(t, service)
			if trial.name == "landed project task" {
				_, err := service.store.Update(
					ctx,
					"task-1",
					func(current taskstate.Task) (taskstate.Task, error) {
						snapshot := current.Snapshot()
						snapshot.Definition.Kind = "task"
						return taskstate.NewMigrated(snapshot)
					},
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if trial.name == "identity missing" {
				_, err := service.store.Update(
					ctx,
					"task-1",
					func(current taskstate.Task) (taskstate.Task, error) {
						snapshot := current.Snapshot()
						snapshot.Worker.AgentArgs = []string{"--model", "model-1"}
						return taskstate.NewMigrated(snapshot)
					},
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Close, "2026-10-20"); err != nil {
				t.Fatal(err)
			}
			if trial.name == "retired worker" {
				if _, err := service.store.Update(ctx, "task-1", func(current taskstate.Task) (taskstate.Task, error) { return current.RetireResult("gen-1") }); err != nil {
					t.Fatal(err)
				}
			}
			runner.absent = trial.name == "closed missing agent" || trial.name == "identity missing"
			runner.unreadable = trial.name == "unreadable endpoint"
			task, err := service.Reopen(ctx, "task-1", herdr.Client{SessionRunner: runner})
			if trial.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), trial.wantError) {
					t.Fatalf("reopen: %v; want %s", err, trial.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if task.State() != taskstate.InFlight || task.Snapshot().Completion != nil ||
				task.Snapshot().Worker.Generation != "gen-1" {
				t.Fatalf("reopen: %+v", task.Snapshot())
			}
			if _, err := service.Reopen(ctx, "task-1", herdr.Client{SessionRunner: runner}); err != nil {
				t.Fatal(err)
			}
			if trial.name == "closed missing agent" {
				worker, err := service.SupervisedWorker(
					"task-1",
					messages,
					herdr.Client{SessionRunner: runner},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := worker.Restart(ctx); err != nil {
					t.Fatal(err)
				}
				current, _ := service.store.Read(ctx, "task-1")
				if current.Snapshot().Worker.Generation == "gen-1" ||
					!strings.Contains(strings.Join(runner.startArgs, " "), "resume thread-1") {
					t.Fatal("restart lost session identity or generation fencing")
				}
			}
		})
	}
}

func TestReopenUnlandedProject(t *testing.T) {
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprint(landed), func(t *testing.T) {
			ctx := context.Background()
			service, runner, _ := runningWorkerService(t)
			project := t.TempDir()
			git := func(directory string, args ...string) string {
				t.Helper()
				data, err := exec.Command("git", append([]string{"-C", directory}, args...)...).
					CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", args, data, err)
				}
				return strings.TrimSpace(string(data))
			}
			git(project, "init", "-b", "main")
			commit := func(directory string) {
				git(
					directory,
					"-c",
					"user.name=Test",
					"-c",
					"user.email=test@example.com",
					"commit",
					"--allow-empty",
					"-m",
					"work",
				)
			}
			commit(project)
			base := git(project, "rev-parse", "HEAD")
			remote := filepath.Join(t.TempDir(), "remote.git")
			git(project, "clone", "--bare", project, remote)
			git(project, "remote", "add", "origin", remote)
			git(project, "worktree", "add", "-b", "task/work", runner.worktree)
			commit(runner.worktree)
			if landed {
				git(project, "push", "origin", "task/work:main")
			}
			_, err := service.store.Update(
				ctx,
				"task-1",
				func(task taskstate.Task) (taskstate.Task, error) {
					snapshot := task.Snapshot()
					snapshot.Worker.Project = project
					snapshot.Worker.Lease = taskstate.WorktreeLease{
						ID:         "lease",
						Holder:     "task-1",
						Project:    project,
						Path:       runner.worktree,
						Branch:     "task/work",
						BaseCommit: base,
						State:      taskstate.LeaseHeld,
					}
					return taskstate.NewMigrated(snapshot)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Finish(ctx, "task-1", "gen-1", taskstate.Close, "2026-10-22"); err != nil {
				t.Fatal(err)
			}
			service.git.Executable, _ = exec.LookPath("git")
			_, err = service.Reopen(ctx, "task-1", herdr.Client{SessionRunner: runner})
			if landed {
				if err == nil || !strings.Contains(err.Error(), "already landed") {
					t.Fatalf("landed reopen: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReopenFollowUpReport(t *testing.T) {
	ctx := context.Background()
	service, messages, assignment := assignmentFixture(t)
	makeScout(t, service)
	first, err := service.SendReply(ctx, messages.Root(), replyInput(assignment))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, messages.Root(), first.Message.ID.String(), taskstate.Close, "2026-10-20"); err != nil {
		t.Fatal(err)
	}
	current, _ := service.store.Read(ctx, "task-1")
	runner := &workerRunner{worktree: current.Snapshot().Worker.Worktree, harness: "codex"}
	client := herdr.Client{SessionRunner: runner}
	if _, err := service.SendAssignment(ctx, messages.Root(), "task-1", "follow-up", "Follow up", client); err == nil ||
		!strings.Contains(err.Error(), "invalid task transition") {
		t.Fatalf("original failure: %v", err)
	}
	t.Log("before: task send: invalid task transition")
	if _, err := service.Reopen(ctx, "task-1", client); err != nil {
		t.Fatal(err)
	}
	followUp, err := service.SendAssignment(
		ctx,
		messages.Root(),
		"task-1",
		"follow-up",
		"Follow up",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.ReadAssignment(
		ctx,
		messages.Root(),
		"task-1",
		followUp.Message.ID.String(),
		"gen-1",
	)
	if err != nil || read.State != AssignmentCurrent {
		t.Fatalf("receive: %+v %v", read, err)
	}
	input := replyInput(followUp.Message)
	input.Key = "follow-up-result"
	input.Body = "Follow-up result"
	reply, err := service.SendReply(ctx, messages.Root(), input)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.ReadReport(ctx, "task-1", reply.Message.ID.String())
	if err != nil || report.Body != input.Body {
		t.Fatalf("report: %+v %v", report, err)
	}
	if _, err := service.Resolve(ctx, messages.Root(), first.Message.ID.String(), taskstate.Close, "2026-10-20"); err == nil {
		t.Fatal("old acceptance closed reopened work")
	}
	if _, err := service.Resolve(ctx, messages.Root(), reply.Message.ID.String(), taskstate.Close, "2026-10-21"); err != nil {
		t.Fatal(err)
	}
	t.Log(
		"after: task reopen -> task send -> receive current -> reply -> saved report -> resolve: success",
	)
}
