package workeraccess

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadSecretFileRequiresPrivateRegularStorage(t *testing.T) {
	_, secret, err := Issue("task-1", "gen-1", Operations())
	if err != nil {
		t.Fatal(err)
	}
	for _, trial := range []string{"private", "public file", "public directory", "symlink", "fifo", "invalid content", "oversize"} {
		t.Run(trial, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "secret")
			data := secret.Bytes()
			switch trial {
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "symlink":
				err = os.WriteFile(filepath.Join(directory, "target"), data, 0600)
				if err == nil {
					err = os.Symlink("target", path)
				}
			default:
				if trial == "invalid content" {
					data = []byte("invalid")
				}
				if trial == "oversize" {
					data = bytes.Repeat([]byte("x"), 300)
				}
				err = os.WriteFile(path, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if trial == "public file" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if trial == "public directory" {
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := ReadSecretFile(path)
			if trial == "private" {
				if err != nil || !bytes.Equal(loaded, secret.Bytes()) {
					t.Fatalf("private secret read failed: %v", err)
				}
				return
			}
			if err == nil || len(loaded) != 0 {
				t.Fatal("unsafe secret storage accepted")
			}
			if strings.Contains(err.Error(), string(secret.Bytes())) {
				t.Fatal("credential error exposed the secret")
			}
		})
	}
}
