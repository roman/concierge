package hostservice

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func TestRecoveryPolicyBeforeReadiness(t *testing.T) {
	for _, policy := range []RecoveryPolicy{"", RecoveryNormal, RecoveryMaintenance} {
		t.Run(string(policy), func(t *testing.T) {
			server, _, _ := taskEndpointFixture(t)
			server.config.Recovery = policy
			directory := t.TempDir()
			if _, err := server.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
				snapshot := task.Snapshot()
				snapshot.Worker.Worktree = directory
				return taskstate.NewMigrated(snapshot)
			}); err != nil {
				t.Fatal(err)
			}
			// A confirmed missing worker reaches Restart during controller preparation.
			var diagnostics bytes.Buffer
			server.config.Diagnostics = &diagnostics
			script := "#!/bin/sh\ncase \"$1 $2\" in\n'pane get') printf '%s\\n' '{\"result\":{\"pane\":{\"pane_id\":\"pane-1\",\"foreground_cwd\":\"/worktree\"}}}' ;;\n'agent get') printf '%s\\n' '{\"error\":{\"code\":\"agent_not_found\"}}'; exit 1 ;;\n*) exit 72 ;;\nesac\n"
			script = strings.ReplaceAll(script, "/worktree", directory)
			if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp("", "recovery-policy-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			listener, err := net.ListenUnix(
				"unix",
				&net.UnixAddr{Name: filepath.Join(dir, "host.sock"), Net: "unix"},
			)
			if err != nil {
				t.Fatal(err)
			}
			ready := errors.New("readiness reached")
			err = server.ServeReady(t.Context(), listener, func() error {
				if policy == RecoveryMaintenance && diagnostics.Len() != 0 {
					t.Errorf("maintenance attempted recovery before readiness: %s", &diagnostics)
				}
				if policy != RecoveryMaintenance &&
					!strings.Contains(diagnostics.String(), "worker restart failed") {
					t.Errorf("normal recovery did not attempt restart: %s", &diagnostics)
				}
				return ready
			})
			if !errors.Is(err, ready) {
				t.Fatal(err)
			}
		})
	}
}

func TestMaintenanceSkipsPendingReconciliation(t *testing.T) {
	server, key, failure := initialRecoveryFixture(t)
	server.config.Recovery = RecoveryMaintenance
	if err := os.Remove(failure); err != nil {
		t.Fatal(err)
	}
	if err := server.controllers.awaitInitialWakeup(key); err != nil {
		t.Fatal(err)
	}
	server.reconcileInitialTasks(t.Context())
	if len(server.controllers.initialWakeupTasks()) != 1 {
		t.Fatal("maintenance reconciled a pending task")
	}
	_, err := server.supervision(t.Context(), Supervise, SupervisionRequest{TaskID: key.id})
	if err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("maintenance admitted task monitoring: %v", err)
	}
	_, err = server.supervision(t.Context(), StopSupervision, SupervisionRequest{TaskID: key.id})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceDiscardKeepsPrimaryAuthority(t *testing.T) {
	_, _, socket := fixture(
		t,
		func(server *Server) { server.config.Recovery = RecoveryMaintenance },
	)
	response, err := Call(
		context.Background(),
		socket,
		Request{Operation: Discard, Transition: &TransitionRequest{
			TaskID: "task-1", Generation: "gen-1", OperatorApproved: true,
		}},
	)
	if err != nil || response.Outcome != Unauthorized {
		t.Fatalf("discard admitted without primary: %+v, %v", response, err)
	}
}

func TestMaintenanceDiscardRetainsGenerationGuard(t *testing.T) {
	server, _, _ := taskEndpointFixture(t)
	server.config.Recovery = RecoveryMaintenance
	if _, err := server.store.Update(t.Context(), "task-1", func(task taskstate.Task) (taskstate.Task, error) {
		snapshot := task.Snapshot()
		worker := snapshot.Worker
		worker.Lease = taskstate.WorktreeLease{ID: "lease", Holder: "holder", Project: worker.Project, Path: worker.Worktree, Branch: "task/work", BaseCommit: strings.Repeat("1", 40), State: taskstate.LeaseHeld}
		return taskstate.NewMigrated(snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	task, err := server.store.Read(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.CheckDiscardWorker("gen-1"); err != nil {
		t.Fatalf("invalid discard fixture: %v", err)
	}
	_, err = server.perform(t.Context(), Request{Operation: Discard, Transition: &TransitionRequest{
		TaskID: "task-1", Generation: "other-generation", OperatorApproved: true,
	}})
	if !errors.Is(err, taskstate.ErrInvalidTransition) {
		t.Fatalf("discard skipped generation guard: %v", err)
	}
	_, err = server.perform(
		t.Context(),
		Request{
			Operation: Discard,
			Transition: &TransitionRequest{
				TaskID:           "task-1",
				Generation:       "gen-1",
				OperatorApproved: true,
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "herdr") {
		t.Fatalf("maintenance bypassed pane absence check: %v", err)
	}
}

func TestRecoveryPolicyValidationAndLegacyStatus(t *testing.T) {
	recipient, _ := inbox.ParseRecipient("concierge")
	for _, policy := range []RecoveryPolicy{"", RecoveryNormal, RecoveryMaintenance, "typo"} {
		t.Run(string(policy), func(t *testing.T) {
			config := Config{
				Root:            "/state",
				MessageRoot:     "/messages",
				Recipient:       recipient,
				HerdrExecutable: "/bin/herdr",
				Supervision:     defaultSupervision(),
				Recovery:        policy,
			}
			if err := config.validate(); (err == nil) != (policy != "typo") {
				t.Fatalf("config policy %q: %v", policy, err)
			}
			response := Response{
				Outcome: Completed,
				Status: &StatusResult{
					Root:        "/state",
					MessageRoot: "/messages",
					Recipient:   "concierge",
					Executable:  "/bin/concierge",
					Recovery:    policy,
				},
			}
			if err := validateStatusResponse(response); (err == nil) != (policy != "typo") {
				t.Fatalf("status policy %q: %v", policy, err)
			}
		})
	}
}

func TestMaintenanceRejectsLaunchBeforeResources(t *testing.T) {
	server, input := launchRequestFixture(t)
	server.config.Recovery = RecoveryMaintenance
	input.Worktree = ""
	server.config.TreehouseExecutable = "/tools/treehouse"
	git := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\ncase \"$3\" in\ncheck-ref-format) exit 0 ;;\ncat-file) printf 'commit\\n'; exit 0 ;;\nesac\nexit 72\n"), 0700); err != nil {
		t.Fatal(err)
	}
	server.config.GitExecutable = git
	marker := filepath.Join(t.TempDir(), "herdr-called")
	if err := os.WriteFile(server.config.HerdrExecutable, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 72\n"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := server.store.Read(t.Context(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.launch(t.Context(), input)
	if err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Errorf("launch was not refused by maintenance: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("maintenance launch invoked Herdr")
	}
	after, err := server.store.Read(t.Context(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
		t.Fatal("maintenance launch changed the task")
	}
}
