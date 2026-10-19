package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskstate"
)

func TestLaunchResolvesMachineLabelAndSendsPrimaryProject(t *testing.T) {
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
	var output bytes.Buffer
	code := Run(ctx, arguments, strings.NewReader("Work remotely."), &output, io.Discard)
	if code != 0 || !strings.Contains(output.String(), "launch: pending") ||
		!strings.Contains(output.String(), `machine: "saved-id"`) {
		t.Fatalf("remote launch code %d: %s", code, &output)
	}
	select {
	case request := <-received:
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
}
