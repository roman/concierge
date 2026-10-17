package workeraccess

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedSecretOperationsDoNotFollowReplacedDirectoryPaths(t *testing.T) {
	base := t.TempDir()
	directory, moved, foreign := filepath.Join(
		base,
		"stage",
	), filepath.Join(
		base,
		"moved",
	), filepath.Join(
		base,
		"foreign",
	)
	for _, path := range []string{directory, foreign} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := OpenPrivateDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	grant, secret, err := Issue("task-one", "generation-one", Operations())
	if err != nil {
		t.Fatal(err)
	}
	otherGrant, otherSecret, err := Issue("task-two", "generation-two", Operations())
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallSecretFile(foreign, bytes.NewReader(otherSecret.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, directory); err != nil {
		t.Fatal(err)
	}
	if err := InstallDirectorySecret(root, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	data, err := ReadSecretFile(filepath.Join(moved, "secret"))
	if err != nil || !bytes.Equal(data, secret.Bytes()) {
		t.Fatal("installed outside the pinned directory")
	}
	if err := RemoveDirectorySecret(root, otherGrant.Verifier); err == nil {
		t.Fatal("removed a different secret")
	}
	if err := RemoveDirectorySecret(root, grant.Verifier); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "secret")); !os.IsNotExist(err) {
		t.Fatal("retained the staging secret")
	}
	data, err = ReadSecretFile(filepath.Join(foreign, "secret"))
	if err != nil || !bytes.Equal(data, otherSecret.Bytes()) {
		t.Fatal("changed a foreign directory")
	}
}
