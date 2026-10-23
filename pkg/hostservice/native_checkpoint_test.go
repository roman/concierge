package hostservice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func checkpointLaunchFixture(t *testing.T, mode string) (*Server, LaunchRequest) {
	t.Helper()
	server, input := launchRequestFixture(t)
	input.Harness = "claude"
	if mode == "codex trust" || mode == "command approval" {
		input.Harness = "codex"
	}
	t.Setenv("CHECKPOINT_HARNESS", input.Harness)
	input.Worktree = t.TempDir()
	t.Setenv("CHECKPOINT_WORKTREE", input.Worktree)
	t.Setenv("CHECKPOINT_MODE", mode)
	t.Setenv("CHECKPOINT_COUNTER", filepath.Join(t.TempDir(), "reads"))
	git := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\ncase \"$5\" in\ncheck-ref-format) exit 0 ;;\ncat-file) printf 'commit\\n'; exit 0 ;;\nesac\nexit 72\n"), 0700); err != nil {
		t.Fatal(err)
	}
	server.config.GitExecutable = git
	task, err := server.reserveInitialLaunch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	generation := task.Snapshot().LaunchIntent.Generation
	for _, advance := range []func(taskstate.Task) (taskstate.Task, error){
		func(task taskstate.Task) (taskstate.Task, error) { return task.BeginLaunchPreparation(generation) },
		func(task taskstate.Task) (taskstate.Task, error) { return task.RequestLaunchPane(generation) },
	} {
		if _, err := server.store.Update(context.Background(), input.TaskID, advance); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.store.RecordLaunchEndpoint(context.Background(), input.TaskID, generation, taskstate.LaunchEndpoint{Workspace: input.Workspace, Tab: "t1", Pane: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.Update(context.Background(), input.TaskID, func(task taskstate.Task) (taskstate.Task, error) { return task.RequestLaunchAgent(generation) }); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1 $2" in
'pane report-metadata') printf '%s' '{"result":{}}' ;;
'pane get')
 workspace=workspace-1
 [ "$CHECKPOINT_MODE" != 'moved pane' ] || workspace=other
 printf '{"result":{"pane":{"pane_id":"p1","workspace_id":"%s","tab_id":"t1","foreground_cwd":"%s"}}}' "$workspace" "$CHECKPOINT_WORKTREE" ;;
 'agent get')
 state=blocked
 if [ "$CHECKPOINT_MODE" = 'clear after notice' ]; then
 count=0
 [ ! -f "$CHECKPOINT_COUNTER" ] || read count < "$CHECKPOINT_COUNTER"
 count=$((count+1))
 printf '%s\n' "$count" > "$CHECKPOINT_COUNTER"
 [ "$count" -le 3 ] || state=idle
 fi
 pane=p1
 [ "$CHECKPOINT_MODE" != 'wrong agent' ] || pane=other
 printf '{"result":{"agent":{"pane_id":"%s","agent":"%s","agent_status":"%s"}}}' "$pane" "$CHECKPOINT_HARNESS" "$state" ;;
'agent read')
 case "$CHECKPOINT_MODE" in
 trust) printf '%s\n' 'Accessing workspace:' 'Quick safety check' 'No, exit' 'Yes, I trust this folder' 'Enter to confirm · Esc to cancel' ;;
 'codex trust') printf 'Folder access\n%s\nTrust this folder?\n1. Trust and continue\n2. Quit\nenter continue · esc quit\n' "$CHECKPOINT_WORKTREE" ;;
 'command approval') printf '%s\n' 'Would you like to run the following command?' '$ git fetch origin main' '1. Yes, proceed (y)' '2. No, and tell Codex what to do differently (esc)' 'Press enter to confirm or esc to cancel' ;;
 'read unavailable') exit 73 ;;
 *) printf '%s\n' 'Permission needed' 'Enter to confirm · Esc to cancel' ;;
 esac ;;
*) exit 74 ;;
esac
`
	if err := os.WriteFile(server.config.HerdrExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return server, input
}

func TestCodexCheckpointNamesFolderTrustAndCommandApproval(t *testing.T) {
	for _, mode := range []string{"codex trust", "command approval"} {
		t.Run(mode, func(t *testing.T) {
			server, input := checkpointLaunchFixture(t, mode)
			checkpoint, err := server.launchNativeCheckpoint(t.Context(), input.TaskID)
			want := "folder_trust"
			if mode == "command approval" {
				want = "command_approval"
			}
			if err != nil || checkpoint == nil || checkpoint.Kind != want {
				t.Fatalf("wrong Codex checkpoint: %+v %v", checkpoint, err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			pending, err := messages.List(server.config.Recipient)
			if err != nil || len(pending) != 1 {
				t.Fatalf("missing checkpoint notice: %v", err)
			}
			var notice struct {
				Help string `json:"help"`
			}
			if err := json.Unmarshal([]byte(pending[0].Body), &notice); err != nil {
				t.Fatal(err)
			}
			detail := input.Worktree
			if mode == "command approval" {
				detail = "git fetch origin main"
			}
			if !strings.Contains(notice.Help, detail) {
				t.Fatalf("checkpoint omitted its scope: %s", notice.Help)
			}
			label := "Folder-trust dialog"
			if mode == "command approval" {
				label = "Command-approval dialog"
			}
			if !strings.Contains(notice.Help, label) {
				t.Fatalf("checkpoint omitted its dialog type: %s", notice.Help)
			}
		})
	}
}

func TestCheckpointCommandPreservesMultilineIndentation(t *testing.T) {
	screen := "  $ echo previous command\nold output\nWould you like to run the following command?\n\n  $ python - <<'PY'\n  if True:\n      print('hello')\n  PY\n\n› 1. Yes, proceed (y)\n  2. No\n"
	want := "python - <<'PY'\nif True:\n    print('hello')\nPY"
	if command := checkpointCommand(screen); command != want {
		t.Fatalf("changed displayed command: %q", command)
	}
	if command := checkpointCommand(strings.ReplaceAll(screen, "print('hello')", "…")); command != "" {
		t.Fatal("reported a clipped command as complete")
	}
}

func TestBlockedLaunchPublishesNativeCheckpoint(t *testing.T) {
	for _, mode := range []string{"trust", "other input", "read unavailable"} {
		t.Run(mode, func(t *testing.T) {
			server, input := checkpointLaunchFixture(t, mode)
			ctx := context.Background()
			before, _ := server.store.Read(ctx, input.TaskID)
			response, err := server.launch(ctx, input)
			if err != nil || response.Outcome != Blocked {
				t.Fatalf("checkpoint launch = %+v, %v", response, err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			pending, err := messages.List(server.config.Recipient)
			if err != nil || len(pending) != 1 {
				t.Fatalf("primary has no native checkpoint notice: %+v, %v", pending, err)
			}
			var notice struct {
				Kind       string `json:"kind"`
				Checkpoint string `json:"checkpoint"`
				Task       string `json:"task"`
				Generation string `json:"generation"`
				Session    string `json:"session"`
				Pane       string `json:"pane"`
				Worktree   string `json:"worktree"`
				Help       string `json:"help"`
			}
			if err := json.Unmarshal([]byte(pending[0].Body), &notice); err != nil {
				t.Fatal(err)
			}
			want := "native_input"
			if mode == "trust" {
				want = "folder_trust"
			}
			if notice.Kind != "task-native-checkpoint" || notice.Checkpoint != want ||
				notice.Task != input.TaskID ||
				notice.Generation != before.Snapshot().LaunchIntent.Generation ||
				notice.Session != "work" ||
				notice.Pane != "p1" ||
				notice.Worktree != input.Worktree {
				t.Fatalf("checkpoint notice = %+v", notice)
			}
			if !strings.Contains(notice.Help, "operator") ||
				!strings.Contains(notice.Help, "native dialog") {
				t.Fatalf("checkpoint lacks a review instruction: %+v", notice)
			}
			after, _ := server.store.Read(ctx, input.TaskID)
			if !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
				t.Fatal("blocked native checkpoint changed the task binding")
			}
			if _, err := server.launch(ctx, input); err != nil {
				t.Fatal(err)
			}
			if err := server.reconcileInitialTask(ctx, controllerKey{kind: taskController, id: input.TaskID}); err == nil {
				t.Fatal("background reconciliation ignored the blocked checkpoint")
			}
			retried, err := messages.List(server.config.Recipient)
			if err != nil || !reflect.DeepEqual(pending, retried) {
				t.Fatalf("checkpoint retry duplicated or changed the notice: %+v, %v", retried, err)
			}
		})
	}
}

func TestCheckpointNoticeFailureRemainsRecoverable(t *testing.T) {
	server, input := checkpointLaunchFixture(t, "trust")
	ctx := context.Background()
	if err := os.WriteFile(server.config.MessageRoot, []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err := server.launch(ctx, input)
	if err != nil || response.Outcome != Blocked || response.Checkpoint == nil ||
		response.Checkpoint.Notification != "failed" ||
		!strings.Contains(response.Error, "notification is unconfirmed") {
		t.Fatalf("lost checkpoint publication failure: %+v, %v", response, err)
	}
	if err := os.Remove(server.config.MessageRoot); err != nil {
		t.Fatal(err)
	}
	if err := server.reconcileInitialTask(ctx, controllerKey{kind: taskController, id: input.TaskID}); err == nil {
		t.Fatal("checkpoint cleared without native input")
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	notices, err := messages.List(server.config.Recipient)
	if err != nil || len(notices) != 1 {
		t.Fatalf("reconciliation lost checkpoint notice: %+v, %v", notices, err)
	}
}

func TestCheckpointDoesNotClassifyAnotherEndpoint(t *testing.T) {
	for _, mode := range []string{"moved pane", "wrong agent"} {
		t.Run(mode, func(t *testing.T) {
			server, input := checkpointLaunchFixture(t, mode)
			response, err := server.launch(context.Background(), input)
			if err != nil || response.Outcome != Blocked || response.Checkpoint != nil {
				t.Fatalf("unrelated endpoint became a native checkpoint: %+v, %v", response, err)
			}
			messages, _ := inbox.New(server.config.MessageRoot)
			notices, err := messages.List(server.config.Recipient)
			if err != nil || len(notices) != 0 {
				t.Fatalf("unrelated endpoint published a checkpoint: %+v, %v", notices, err)
			}
		})
	}
}

func TestCheckpointResponseRejectsCompletedOrUnrelatedData(t *testing.T) {
	server, input := checkpointLaunchFixture(t, "trust")
	response, err := server.launch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Operation: Launch, Launch: &input}
	if err := validateResponse(response, request); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unknown kind", "unknown notification", "pending", "unrelated", "completed launch", "missing transition"} {
		t.Run(change, func(t *testing.T) {
			candidate := response
			checkpoint := *response.Checkpoint
			candidate.Checkpoint = &checkpoint
			checkRequest := request
			switch change {
			case "unknown kind":
				checkpoint.Kind = "automatic_trust"
			case "unknown notification":
				checkpoint.Notification = "unknown"
			case "pending":
				candidate.Outcome = Pending
			case "unrelated":
				checkRequest = Request{Operation: HostStatus}
				candidate.Outcome = Completed
			case "missing transition":
				candidate.Transition = nil
			case "completed launch":
				snapshot := *candidate.Transition
				intent := snapshot.LaunchIntent
				snapshot.LaunchIntent = nil
				snapshot.State = taskstate.InFlight
				snapshot.Worker = &taskstate.Worker{
					Name:       intent.Name,
					Harness:    intent.Harness,
					Session:    intent.Session,
					Workspace:  intent.Endpoint.Workspace,
					Tab:        intent.Endpoint.Tab,
					Pane:       intent.Endpoint.Pane,
					Project:    intent.Project,
					Worktree:   intent.Worktree,
					Generation: intent.Generation,
					AgentArgs:  []string{"--resume", "native-session"},
				}
				candidate.Transition = &snapshot
			}
			if err := validateResponse(candidate, checkRequest); err == nil {
				t.Fatalf("accepted %s native checkpoint", change)
			}
		})
	}
}

func TestCheckpointDoesNotMaskHeldLaunch(t *testing.T) {
	server, input := checkpointLaunchFixture(t, "trust")
	ctx := context.Background()
	if _, err := server.store.Update(ctx, input.TaskID, func(task taskstate.Task) (taskstate.Task, error) {
		return task.PlaceHold(taskstate.Hold{Kind: "primary", Reason: "Wait for review"})
	}); err != nil {
		t.Fatal(err)
	}
	response, err := server.launch(ctx, input)
	if err != nil || response.Outcome != Blocked || response.Checkpoint != nil {
		t.Fatalf("task hold was masked by native checkpoint: %+v, %v", response, err)
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	notices, err := messages.List(server.config.Recipient)
	if err != nil || len(notices) != 0 {
		t.Fatalf("held launch published a native checkpoint: %+v, %v", notices, err)
	}
}

func TestCheckpointClassificationUpgradeIsIdempotent(t *testing.T) {
	server, input := checkpointLaunchFixture(t, "read unavailable")
	ctx := context.Background()
	if _, err := server.launch(ctx, input); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHECKPOINT_MODE", "trust")
	for range 2 {
		if err := server.reconcileInitialTask(ctx, controllerKey{kind: taskController, id: input.TaskID}); err == nil {
			t.Fatal("native trust checkpoint cleared automatically")
		}
	}
	messages, _ := inbox.New(server.config.MessageRoot)
	notices, err := messages.List(server.config.Recipient)
	if err != nil || len(notices) != 2 {
		t.Fatalf("classification upgrade notices = %+v, %v", notices, err)
	}
	kinds := map[string]bool{}
	for _, message := range notices {
		var notice struct {
			Checkpoint string `json:"checkpoint"`
		}
		if err := json.Unmarshal([]byte(message.Body), &notice); err != nil {
			t.Fatal(err)
		}
		kinds[notice.Checkpoint] = true
	}
	if !kinds["native_input"] || !kinds["folder_trust"] {
		t.Fatalf("classification upgrade lost its kind: %+v", kinds)
	}
}

func TestCheckpointClearedBeforeProgressReadRemainsValid(t *testing.T) {
	server, input := checkpointLaunchFixture(t, "clear after notice")
	response, err := server.launch(context.Background(), input)
	if err != nil || response.Outcome != Blocked || response.Checkpoint == nil {
		t.Fatalf("checkpoint race lost its reported outcome: %+v, %v", response, err)
	}
	if err := validateResponse(response, Request{Operation: Launch, Launch: &input}); err != nil {
		t.Fatalf("checkpoint race produced an invalid host response: %v", err)
	}
}

func TestRemoteCheckpointHelpNamesTheSavedMachine(t *testing.T) {
	for _, machine := range []string{"machine-one", "machine-two"} {
		intent := &taskstate.LaunchIntent{
			Machine:  machine,
			Session:  "default",
			Worktree: "/remote/slot",
			Endpoint: &taskstate.LaunchEndpoint{Pane: "same-pane"},
		}
		help := NativeCheckpointHelp(intent)
		if !strings.Contains(
			help,
			`herdr --machine "`+machine+`" agent read "same-pane" --source visible`,
		) ||
			strings.Contains(help, "--session") {
			t.Fatalf("wrong remote read command: %s", help)
		}
	}
}
