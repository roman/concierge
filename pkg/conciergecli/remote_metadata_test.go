package conciergecli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

func TestRemoteWorkerMetadataCommands(t *testing.T) {
	id := "k-" + strings.Repeat("a", 64)
	bootstrap := &taskops.BootstrapInfo{
		Kind:       "task-bootstrap",
		Task:       "task-1",
		Generation: "test-generation",
	}
	settings := &taskops.WorkerSettings{Repo: "repo", Merge: "off", Delivery: "pr"}
	for _, trial := range []struct {
		name      string
		args      []string
		operation workeraccess.Operation
		response  workerservice.Response
		want      string
	}{
		{"pending", []string{"task", "inbox", "list", "task-1"}, workeraccess.ListPending,
			workerservice.Response{Outcome: workerservice.Completed, Pending: []taskops.PendingMessage{{ID: id, Key: "assignment", CreatedAt: time.Now()}}}, "count: 1"},
		{"bootstrap read", []string{"task", "bootstrap", "read", "task-1", id, "--generation", "test-generation"}, workeraccess.ReadBootstrap,
			workerservice.Response{Outcome: workerservice.Completed, Bootstrap: bootstrap}, "kind: task-bootstrap"},
		{"bootstrap ack", []string{"task", "bootstrap", "ack", "task-1", id, "--generation", "test-generation"}, workeraccess.AckBootstrap,
			workerservice.Response{Outcome: workerservice.Completed, Bootstrap: bootstrap}, "status: acknowledged"},
		{"settings", []string{"task", "settings", "task-1", "--generation", "test-generation"}, workeraccess.ReadSettings,
			workerservice.Response{Outcome: workerservice.Completed, Settings: settings}, "repo: \"repo\""},
		{"project", []string{"project", "show", "repo"}, workeraccess.ReadSettings,
			workerservice.Response{Outcome: workerservice.Completed, Settings: settings}, "delivery: pr"},
	} {
		t.Run(trial.name, func(t *testing.T) {
			requests := remoteCLI(t, trial.response)
			var output bytes.Buffer
			if code := Run(context.Background(), trial.args, strings.NewReader(""), &output, &output); code != 0 ||
				!strings.Contains(output.String(), trial.want) {
				t.Fatalf("remote metadata: code=%d %s", code, &output)
			}
			request := <-requests
			if request.Operation != trial.operation || request.Task != "task-1" ||
				request.Generation != "test-generation" {
				t.Fatalf("wrong remote metadata scope: %+v", request)
			}
		})
	}
}
