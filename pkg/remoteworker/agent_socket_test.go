package remoteworker

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestAgentSocketPreparationPreservesActiveAndForeignPaths(t *testing.T) {
	for _, kind := range []string{"active", "stale", "regular", "symlink", "wrong owner"} {
		t.Run(kind, func(t *testing.T) {
			directory, owner := socketOwnerFixture(t)
			if err := ClaimSocketDirectory(directory, owner); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "agent.sock")
			switch kind {
			case "regular":
				if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("secret", path); err != nil {
					t.Fatal(err)
				}
			default:
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				listener.SetUnlinkOnClose(false)
				defer listener.Close()
				if kind != "active" {
					if err := listener.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "wrong owner" {
				owner.Generation = "different-generation"
			}
			err = PrepareAgentSocket(t.Context(), directory, owner)
			if kind == "stale" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("owned stale agent socket remains")
				}
				return
			}
			if err == nil {
				t.Fatal("active or foreign agent socket accepted")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("active or foreign path changed")
			}
		})
	}
}

func TestAgentProbeUsesOwnedSocketAndRequiresLoadedKey(t *testing.T) {
	directory, owner := socketOwnerFixture(t)
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "agent.sock")
	agent := exec.Command("ssh-agent", "-D", "-a", socket)
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Process.Kill(); _ = agent.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test agent did not create its socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Setenv("SSH_AUTH_SOCK", "/absent/ambient-agent.sock")
	if err := ProbeAgentSocket(t.Context(), directory, owner); !errors.Is(
		err,
		ErrTunnelUnreachable,
	) {
		t.Fatalf("empty agent was considered usable: %v", err)
	}
	key := filepath.Join(directory, "test-key")
	if data, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("test key: %s %v", data, err)
	}
	load := exec.Command("ssh-add", key)
	load.Env = append(sshcommand.ConnectionEnvironment(os.Environ()), "SSH_AUTH_SOCK="+socket)
	if data, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load test key: %s %v", data, err)
	}
	if err := ProbeAgentSocket(t.Context(), directory, owner); err != nil {
		t.Fatal(err)
	}
	owner.Generation = "different-generation"
	if err := ProbeAgentSocket(t.Context(), directory, owner); !errors.Is(err, ErrSocketOwnership) {
		t.Fatalf("agent probe accepted foreign ownership: %v", err)
	}
}
