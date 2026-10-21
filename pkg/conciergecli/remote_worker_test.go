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

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

func remoteCLI(t *testing.T, response workerservice.Response) <-chan workerservice.Request {
	t.Helper()
	directory, err := os.MkdirTemp("", "remote-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	grant, secret, err := workeraccess.Issue("task-1", "test-generation", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(directory, "secret")
	if err := os.WriteFile(secretFile, secret.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replyoutbox.InstallContext(directory, replyoutbox.Context{
		Format: 1, Root: "/primary/state", Recipient: "concierge", Task: grant.Task,
		Generation: grant.Generation, Verifier: grant.Verifier,
	}); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCIERGE_WORKER_SOCKET", socket)
	t.Setenv("CONCIERGE_WORKER_SECRET_FILE", secretFile)
	t.Setenv("CONCIERGE_WORKER_TASK", "task-1")
	t.Setenv(workerLaunchGeneration, "test-generation")
	t.Setenv("CONCIERGE_HOME", filepath.Join(directory, "no-local-state"))
	t.Setenv("TINCAN_HOME", filepath.Join(directory, "no-local-messages"))
	requests := make(chan workerservice.Request, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		respondRemoteCLI(connection, response, requests)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return requests
}

func respondRemoteCLI(
	connection io.ReadWriter,
	response workerservice.Response,
	requests chan<- workerservice.Request,
) {
	var request workerservice.Request
	decoder := json.NewDecoder(connection)
	if decoder.Decode(&request) != nil {
		return
	}
	// Match request framing so the fixture cannot close before the client's half-close.
	if decoder.Decode(new(any)) != io.EOF {
		return
	}
	requests <- request
	_ = json.NewEncoder(connection).Encode(response)
}

func TestIncompleteRemoteModeCannotInitializeLocalState(t *testing.T) {
	t.Setenv("CONCIERGE_WORKER_SOCKET", "")
	root := filepath.Join(t.TempDir(), "must-stay-absent")
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"--root", root, "state", "init"},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code == 0 || !strings.Contains(output.String(), "remote worker") {
		t.Fatalf("incomplete remote mode used local commands: code=%d %s", code, &output)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("incomplete remote mode touched local state: %v", err)
	}
}

func TestRemoteReceiveUsesSocketAndInheritedGeneration(t *testing.T) {
	requests := remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed,
		Assignment: &workerservice.Assignment{
			Status: taskops.AssignmentCurrent,
			Body:   "Remote work",
		}})
	id := "k-" + strings.Repeat("a", 64)
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"task", "receive", "task-1", id, "--generation", "test-generation"},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code != 0 || !strings.Contains(output.String(), "body: \"Remote work\"") {
		t.Fatalf("remote receive: code=%d %s", code, &output)
	}
	request := <-requests
	if request.Task != "task-1" || request.Generation != "test-generation" ||
		request.MessageID != id ||
		request.Operation != workeraccess.Receive {
		t.Fatalf("wrong remote request scope: %+v", request)
	}
	if strings.Contains(output.String(), request.Secret) {
		t.Fatal("remote command exposed its secret")
	}
}

func TestRemoteReplyReadsBodyBeforeRPC(t *testing.T) {
	id := "k-" + strings.Repeat("b", 64)
	requests := remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed,
		Reply: &workerservice.Reply{MessageID: id, Assignment: taskops.ReplyAcknowledged,
			Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed,
			Root: "/primary/state", Recipient: "concierge"}})
	file := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(file, []byte("Completed remotely"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{
			"task",
			"reply",
			"send",
			"task-1",
			"--generation",
			"test-generation",
			"--key",
			"result",
			"--assignment-id",
			id,
			"--body-file",
			file,
		},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code != 0 {
		t.Fatalf("remote reply: code=%d %s", code, &output)
	}
	request := <-requests
	data, _ := json.Marshal(request)
	if request.Body != "Completed remotely" || request.Key != "result" ||
		bytes.Contains(data, []byte(file)) {
		t.Fatal("remote reply did not transmit only body content")
	}
}

func TestRemoteWorkerRejectsAuthorityAndBindingChangesBeforeRPC(t *testing.T) {
	id := "k-" + strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"state", "init"},
		{"host", "stop"},
		{"project", "set", "repo", "--merge", "on"},
		{"machine", "check", "dev-remote"},
		{"machine", "status", "dev-remote"},
		{"machine", "prepare", "dev-remote"},
		{"task", "receive", "other-task", id, "--generation", "test-generation"},
		{"task", "receive", "task-1", id, "--generation", "other-generation"},
		{"--root", "/other", "task", "receive", "task-1", id, "--generation", "test-generation"},
		{"--message-root", "/other", "task", "receive", "task-1", id, "--generation", "test-generation"},
		{"--recipient", "other", "task", "receive", "task-1", id, "--generation", "test-generation"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			requests := remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed})
			var output bytes.Buffer
			if code := Run(context.Background(), args, strings.NewReader(""), &output, &output); code == 0 {
				t.Fatalf("remote authority change accepted: %s", &output)
			}
			select {
			case <-requests:
				t.Fatal("invalid remote operation reached the primary")
			default:
			}
		})
	}
}
