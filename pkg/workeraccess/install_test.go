package workeraccess

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInstallSecretPublishesPrivateFileWithoutReplacement(t *testing.T) {
	_, secret, err := Issue("task-1", "gen-1", Operations())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "task")
	if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "secret")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatalf("identical delivery retry: %v", err)
	}
	after, _ := os.Stat(path)
	loaded, err := ReadSecretFile(path)
	if err != nil || !bytes.Equal(loaded, secret.Bytes()) || !os.SameFile(before, after) {
		t.Fatalf("delivery replaced the secret: %v", err)
	}
	_, other, _ := Issue("task-1", "gen-1", Operations())
	if err := InstallSecretFile(directory, bytes.NewReader(other.Bytes())); err == nil {
		t.Fatal("delivery replaced a different existing secret")
	}
	loaded, err = ReadSecretFile(path)
	if err != nil || !bytes.Equal(loaded, secret.Bytes()) {
		t.Fatal("failed delivery changed the original secret")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "secret" {
		t.Fatalf("delivery retained temporary files: %v", err)
	}
}

func TestConcurrentIdenticalSecretDeliveryHasOneFile(t *testing.T) {
	_, secret, err := Issue("task-1", "gen-1", Operations())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "task")
	var running sync.WaitGroup
	for range 8 {
		running.Go(func() {
			if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
				t.Errorf("concurrent delivery: %v", err)
			}
		})
	}
	running.Wait()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("concurrent deliveries left extra files: %v", err)
	}
}

func TestSecretDeliveryRejectsUnsafeDirectoryAndInput(t *testing.T) {
	_, secret, _ := Issue("task-1", "gen-1", Operations())
	directory := t.TempDir()
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err == nil {
		t.Fatal("delivery accepted a public task directory")
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	for _, data := range [][]byte{nil, []byte("invalid"), bytes.Repeat([]byte("x"), 300)} {
		if err := InstallSecretFile(missing, bytes.NewReader(data)); err == nil {
			t.Fatal("delivery accepted invalid secret input")
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("invalid input created a task directory: %v", err)
		}
	}
}
