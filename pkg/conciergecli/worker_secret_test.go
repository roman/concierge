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

func TestWorkerSecretHelperUsesStdinAndVerifierBoundRemoval(t *testing.T) {
	grant, secret, err := workeraccess.Issue("task-1", "test-generation", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "task")
	var output bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"worker-secret", "install", "--directory", directory},
		bytes.NewReader(secret.Bytes()),
		&output,
		&output,
	)
	if code != 0 || output.String() != "status: installed\n" ||
		bytes.Contains(output.Bytes(), secret.Bytes()) {
		t.Fatalf("stdin secret installation: code=%d %s", code, &output)
	}
	if data, err := workeraccess.ReadSecretFile(filepath.Join(directory, "secret")); err != nil ||
		!bytes.Equal(data, secret.Bytes()) {
		t.Fatalf("helper did not install its stdin content: %v", err)
	}
	output.Reset()
	code = Run(
		context.Background(),
		[]string{"worker-secret", "remove", "--directory", directory, "--verifier", grant.Verifier},
		strings.NewReader(""),
		&output,
		&output,
	)
	if code != 0 || output.String() != "status: removed\n" {
		t.Fatalf("secret removal: code=%d %s", code, &output)
	}
	if _, err := os.Lstat(filepath.Join(directory, "secret")); !os.IsNotExist(err) {
		t.Fatalf("helper retained the secret: %v", err)
	}
}
