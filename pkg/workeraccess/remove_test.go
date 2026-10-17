package workeraccess

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretRemovalRequiresExactVerifier(t *testing.T) {
	grant, secret, err := Issue("task-1", "gen-1", Operations())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "task")
	if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(directory, "keep")
	if err := os.WriteFile(other, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, verifier := range []string{"invalid", strings.Repeat("0", 64)} {
		if err := RemoveSecretFile(directory, verifier); err == nil {
			t.Fatal("removal accepted an unbound verifier")
		}
		if current, err := ReadSecretFile(filepath.Join(directory, "secret")); err != nil ||
			!bytes.Equal(current, secret.Bytes()) {
			t.Fatal("failed removal changed the secret")
		}
	}
	for range 2 {
		if err := RemoveSecretFile(directory, grant.Verifier); err != nil {
			t.Fatalf("idempotent removal failed: %v", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(directory, "secret")); !os.IsNotExist(err) {
		t.Fatalf("removal retained secret: %v", err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "retained" {
		t.Fatalf("removal changed unrelated files: %v", err)
	}
}

func TestSecretRemovalRefusesSymlinkAndPublicDirectory(t *testing.T) {
	grant, secret, _ := Issue("task-1", "gen-1", Operations())
	for _, kind := range []string{"symlink", "public directory"} {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "task")
			if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "secret")
			if kind == "symlink" {
				if err := os.Rename(path, filepath.Join(directory, "target")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target", path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(directory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := RemoveSecretFile(directory, grant.Verifier); err == nil {
				t.Fatal("uncertain secret ownership permitted removal")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("refused removal changed the path: %v", err)
			}
		})
	}
}
