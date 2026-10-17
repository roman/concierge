package conciergecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/replyoutbox"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerContextHelperBindsMetadataWithoutOpeningPrimaryRoots(t *testing.T) {
	grant, secret, _ := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	directory := filepath.Join(t.TempDir(), "remote-task")
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	primaryRoot := filepath.Join(t.TempDir(), "primary-must-stay-absent")
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"worker-context", "install", "--directory", directory,
			"--state-root", primaryRoot, "--task", "task-1", "--generation", "gen-1",
			"--primary-recipient", "concierge", "--verifier", grant.Verifier},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code != 0 || output.String() != "status: bound\n" {
		t.Fatalf("reply context installation: code=%d %s", code, &output)
	}
	binding, err := replyoutbox.ReadContext(directory)
	if err != nil || binding.Root != primaryRoot || binding.Recipient != "concierge" {
		t.Fatalf("wrong reply context metadata: %+v %v", binding, err)
	}
	if _, err := os.Lstat(primaryRoot); !os.IsNotExist(err) {
		t.Fatalf("context metadata selected a primary filesystem path: %v", err)
	}
}

func TestRemoteWorkerCannotInstallReplyContext(t *testing.T) {
	t.Setenv("CONCIERGE_WORKER_TASK", "task-1")
	directory := filepath.Join(t.TempDir(), "must-stay-absent")
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"worker-context", "install", "--directory", directory,
			"--state-root", "/primary/state", "--task", "task-1", "--generation", "gen-1",
			"--primary-recipient", "concierge", "--verifier", strings.Repeat("0", 64)},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code == 0 {
		t.Fatal("remote worker installed its own reply context")
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("remote setup changed the filesystem: %v", err)
	}
}
