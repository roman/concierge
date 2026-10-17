package remoteworker

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

func socketOwnerFixture(t *testing.T) (string, SocketOwner) {
	t.Helper()
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp("", "socket-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	directory := filepath.Join(parent, "task")
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	return directory, SocketOwner{Task: "task-1", Generation: "gen-1", Verifier: grant.Verifier}
}

func TestSocketOwnershipIsBoundToTaskGenerationAndVerifier(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "socket-owner.json")
	before, err := os.Lstat(marker)
	if err != nil || before.Mode().Perm() != 0600 {
		t.Fatalf("ownership marker missing or public: %v", err)
	}
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatalf("idempotent ownership claim: %v", err)
	}
	after, _ := os.Lstat(marker)
	if !os.SameFile(before, after) {
		t.Fatal("identical claim replaced ownership evidence")
	}
	for _, other := range []SocketOwner{
		{Task: "task-2", Generation: owner.Generation, Verifier: owner.Verifier},
		{Task: owner.Task, Generation: "gen-2", Verifier: owner.Verifier},
		{Task: owner.Task, Generation: owner.Generation, Verifier: strings.Repeat("0", 64)},
	} {
		if err := ClaimSocketDirectory(directory, other); err == nil {
			t.Fatal("ownership changed to a different task, generation or verifier")
		}
	}
}

func TestSocketClaimRefusesExistingUnownedSocket(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	path := filepath.Join(directory, "worker.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Lstat(path)
	if err := ClaimSocketDirectory(directory, owner); err == nil {
		t.Fatal("an unowned socket was claimed")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("unowned socket was changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "socket-owner.json")); !os.IsNotExist(err) {
		t.Fatalf("claim created ownership evidence for an unknown socket: %v", err)
	}
}

func TestInitialSocketClaimRequiresTheMatchingPrivateSecret(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	owner.Verifier = strings.Repeat("0", 64)
	if err := ClaimSocketDirectory(directory, owner); err == nil {
		t.Fatal("initial claim ignored the task secret")
	}
	if _, err := os.Lstat(filepath.Join(directory, "socket-owner.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid claim created a marker: %v", err)
	}
}
