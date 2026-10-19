package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestLaunchResolvesMachineLabelAndSendsPrimaryProject(t *testing.T) {
	request := testRemoteLaunchRequest(t, nil)
	if strings.Contains(strings.Join(request.Launch.AgentArgs, " "), "approvals_reviewer") {
		t.Fatal("default launch changed the native reviewer")
	}
}

func TestLaunchNamedAutomaticReviewIsExplicitAndCaptured(t *testing.T) {
	request := testRemoteLaunchRequest(t, []string{"--codex-approval-reviewer=auto-review"})
	want := []string{"--model", "example,model", "-c", "approvals_reviewer=auto_review"}
	if !reflect.DeepEqual(request.Launch.AgentArgs, want) {
		t.Fatalf("automatic review was not captured in the launch: %v", request.Launch.AgentArgs)
	}
}

func TestLaunchNamedAutomaticReviewRejectsIncompatibleLaunches(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		root := shortTaskRoot(t)
		arguments := append(launchArguments(root), "--codex-approval-reviewer=auto-review")
		for index, argument := range arguments {
			if argument == "--harness" {
				arguments[index+1] = harness
			}
		}
		if harness == "claude" {
			arguments = append(arguments, "--machine=baker")
		}
		var output bytes.Buffer
		code := Run(t.Context(), arguments, strings.NewReader("Work"), &output, io.Discard)
		if code != 2 || !strings.Contains(output.String(), "requires a remote Codex launch") {
			t.Fatalf("invalid reviewer selection reached dependencies: %d %s", code, &output)
		}
	}
}

func TestLaunchHelpDocumentsApprovalReviewerDefault(t *testing.T) {
	var output bytes.Buffer
	code := Run(t.Context(), []string{"launch", "--help"}, nil, &output, io.Discard)
	help := strings.Join(strings.Fields(output.String()), " ")
	if code != 0 || !strings.Contains(help, `--codex-approval-reviewer="native"`) ||
		!strings.Contains(strings.ToLower(help), "native preserves current settings") {
		t.Fatalf("reviewer option missing from help: %d %s", code, &output)
	}
}

func testRemoteLaunchRequest(t *testing.T, additional []string) hostservice.Request {
	t.Helper()
	root := shortTaskRoot(t)
	herdr := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
[ "$1 $2" = 'machine list' ] || exit 73
printf '%s' '[{"id":"saved-id","label":"dev-remote","target":"synthetic-target","session":"remote-session","enabled":true}]'
`
	if err := os.WriteFile(herdr, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix(
		"unix",
		&net.UnixAddr{Name: filepath.Join(root, "host.sock"), Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	received := make(chan hostservice.Request, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			result <- err
			return
		}
		defer connection.Close()
		var request hostservice.Request
		if err := json.NewDecoder(connection).Decode(&request); err != nil {
			result <- err
			return
		}
		received <- request
		input := request.Launch
		location := taskstate.RemoteLease{
			Machine: "saved-id", Target: "synthetic-target", PrimaryRoot: root,
			PrimaryProject: input.Project, Base: "/remote/base", Repository: "repo",
			ClientCommit: strings.Repeat("b", 40),
		}
		task, err := taskstate.NewQueued(
			taskstate.Definition{ID: input.TaskID, Title: "Work", Repo: "repo", Kind: "task"},
		)
		if err == nil {
			task, err = task.ReserveInitialLaunch(taskstate.LaunchIntent{
				Machine: location.Machine, Remote: location, Session: "remote-session",
				Generation: "gen-1", Name: "worker", Label: "launch-one",
				Harness: input.Harness, Project: location.ProjectPath(), Branch: input.Branch,
				BaseCommit: input.BaseCommit, Workspace: input.Workspace,
				AgentArgs: input.AgentArgs, Body: input.Body,
			})
		}
		if err != nil {
			result <- err
			return
		}
		snapshot := task.Snapshot()
		result <- json.NewEncoder(connection).Encode(hostservice.Response{
			Outcome: hostservice.Pending, Error: "import confirmation pending", Transition: &snapshot,
		})
	}()
	arguments := append(launchArguments(root), "--machine", "dev-remote", "--herdr", herdr)
	arguments = append(arguments, additional...)
	var output bytes.Buffer
	code := Run(ctx, arguments, strings.NewReader("Work remotely."), &output, io.Discard)
	if code != 0 || !strings.Contains(output.String(), "launch: pending") ||
		!strings.Contains(output.String(), `machine: "saved-id"`) {
		t.Fatalf("remote launch code %d: %s", code, &output)
	}
	var request hostservice.Request
	select {
	case request = <-received:
		if request.Launch.Machine != "saved-id" || request.Launch.Project != "/repo" ||
			request.Launch.Worktree != "" {
			t.Fatalf("remote request: %+v", request.Launch)
		}
	case <-ctx.Done():
		t.Fatal("CLI did not send the remote request")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	return request
}
