package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

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
	var output bytes.Buffer
	code := Run(context.Background(), []string{"worker-socket", "prepare", "--directory", directory,
		"--task", "task-1", "--generation", "gen-1", "--verifier", strings.Repeat("0", 64)},
		strings.NewReader(""), &output, &output)
	if code == 0 {
		t.Fatal("remote worker was allowed to prepare its socket")
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("remote preparation changed the filesystem: %v", err)
	}
}
