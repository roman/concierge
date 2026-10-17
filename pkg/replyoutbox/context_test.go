package replyoutbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

func replyContextFixture(t *testing.T) (string, Context, workeraccess.Secret) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "task")
	grant, secret, err := workeraccess.Issue("task-1", "gen-1", workeraccess.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if err := workeraccess.InstallSecretFile(directory, bytes.NewReader(secret.Bytes())); err != nil {
		t.Fatal(err)
	}
	return directory, Context{Format: 1, Root: "/primary/state", Recipient: "concierge",
		Task: grant.Task, Generation: grant.Generation, Verifier: grant.Verifier}, secret
}

func TestReplyContextBindsImmutableMetadataToThePrivateTaskSecret(t *testing.T) {
	directory, expected, secret := replyContextFixture(t)
	if err := InstallContext(directory, expected); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "reply-context.json")
	before, err := os.Lstat(path)
	if err != nil || before.Mode().Perm() != 0600 {
		t.Fatalf("reply context missing or public: %v", err)
	}
	if err := InstallContext(directory, expected); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Lstat(path)
	if !os.SameFile(before, after) {
		t.Fatal("identical setup replaced reply context")
	}
	actual, err := ReadContext(directory)
	if err != nil || actual != expected {
		t.Fatalf("reply context differs: %+v %v", actual, err)
	}
	if err := actual.Match("task-1", "gen-1", secret.Bytes()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, secret.Bytes()) {
		t.Fatal("reply context serialized the task secret")
	}
	changed := expected
	changed.Root = "/other/state"
	if err := InstallContext(directory, changed); !errors.Is(err, ErrContext) {
		t.Fatalf("another root replaced the original binding: %v", err)
	}
}

func TestReplyContextRefusesWrongTaskGenerationAndSecret(t *testing.T) {
	directory, expected, secret := replyContextFixture(t)
	if _, err := ReadContext(directory); !errors.Is(err, ErrContext) {
		t.Fatalf("missing context has no recovery error: %v", err)
	}
	for _, binding := range []struct {
		task, generation string
		secret           []byte
	}{
		{"other-task", "gen-1", secret.Bytes()},
		{"task-1", "other-generation", secret.Bytes()},
		{"task-1", "gen-1", []byte("incorrect")},
	} {
		if err := expected.Match(binding.task, binding.generation, binding.secret); !errors.Is(
			err,
			ErrContext,
		) {
			t.Fatalf("reply context accepted another launch: %v", err)
		}
	}
	expected.Verifier = strings.Repeat("0", 64)
	if err := InstallContext(directory, expected); !errors.Is(err, ErrContext) {
		t.Fatalf("context installation ignored its task secret: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "reply-context.json")); !os.IsNotExist(err) {
		t.Fatalf("wrong secret created reply metadata: %v", err)
	}
}
