package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerSocketProbeReportsMissingForward(t *testing.T) {
	grant, secret, _ := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	directory := filepath.Join(t.TempDir(), "task")
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	owner := remoteworker.SocketOwner{
		Task:       grant.Task,
		Generation: grant.Generation,
		Verifier:   grant.Verifier,
	}
	if err := remoteworker.ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := Run(context.Background(), []string{"worker-socket", "probe", "--directory", directory,
		"--task", "task-1", "--generation", "gen-1", "--verifier", grant.Verifier},
		strings.NewReader(""), &output, &output)
	if code != 1 || !strings.Contains(output.String(), remoteworker.ErrTunnelUnreachable.Error()) {
		t.Fatalf("missing forward was not unreachable: code=%d %s", code, &output)
	}
}

func TestWorkerSocketHelperRequiresMatchingOwnership(t *testing.T) {
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "task")
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"worker-socket", "prepare", "--directory", directory,
		"--task", "task-1", "--generation", "gen-1", "--verifier", grant.Verifier}
	var output bytes.Buffer
	code := Run(context.Background(), arguments, strings.NewReader(""), &output, &output)
	if code != 0 || output.String() != "status: prepared\n" {
		t.Fatalf("socket preparation: code=%d %s", code, &output)
	}
	if _, err := os.Lstat(filepath.Join(directory, "socket-owner.json")); err != nil {
		t.Fatal(err)
	}
	arguments[len(arguments)-1] = strings.Repeat("0", 64)
	output.Reset()
	if code := Run(context.Background(), arguments, strings.NewReader(""), &output, &output); code == 0 {
		t.Fatal("socket preparation accepted a different owner")
	}
}

func TestRemoteWorkerCannotPrepareSocket(t *testing.T) {
	t.Setenv("CONCIERGE_WORKER_TASK", "task-1")
	directory := filepath.Join(t.TempDir(), "absent")
	for _, operation := range []string{"prepare", "probe"} {
		var output bytes.Buffer
		code := Run(
			context.Background(),
			[]string{"worker-socket", operation, "--directory", directory,
				"--task", "task-1", "--generation", "gen-1", "--verifier", strings.Repeat("0", 64)},
			strings.NewReader(""),
			&output,
			&output,
		)
		if code == 0 {
			t.Fatalf("remote worker was allowed to %s its socket", operation)
		}
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("remote preparation changed the filesystem: %v", err)
	}
}
