package workeraccess

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestGrantBindsSecretToTaskGenerationAndOperations(t *testing.T) {
	grant, secret, err := Issue("task-1", "gen-1", []Operation{Receive, SendReply})
	if err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct {
		name       string
		task       string
		generation string
		operation  Operation
		secret     []byte
		want       error
	}{
		{"current", "task-1", "gen-1", Receive, secret.Bytes(), nil},
		{"reply", "task-1", "gen-1", SendReply, secret.Bytes(), nil},
		{"wrong secret", "task-1", "gen-1", Receive, []byte("wrong"), ErrDenied},
		{"wrong task", "task-2", "gen-1", Receive, secret.Bytes(), ErrDenied},
		{"wrong generation", "task-1", "gen-2", Receive, secret.Bytes(), ErrDenied},
		{"ungranted operation", "task-1", "gen-1", ReadSettings, secret.Bytes(), ErrDenied},
		{"primary operation", "task-1", "gen-1", Operation("land"), secret.Bytes(), ErrDenied},
	} {
		t.Run(trial.name, func(t *testing.T) {
			if err := grant.Authorize(trial.task, trial.generation, trial.operation, trial.secret); !errors.Is(
				err,
				trial.want,
			) {
				t.Fatalf("authorization: %v, want %v", err, trial.want)
			}
		})
	}
	grant.Revoked = true
	if err := grant.Authorize("task-1", "gen-1", Receive, secret.Bytes()); !errors.Is(
		err,
		ErrDenied,
	) {
		t.Fatalf("revoked authorization: %v", err)
	}
}

func TestGrantPersistsOnlyVerifierAndOwnsItsOperations(t *testing.T) {
	operations := []Operation{Receive}
	grant, secret, err := Issue("task-1", "gen-1", operations)
	if err != nil {
		t.Fatal(err)
	}
	operations[0] = SendReply
	data, err := json.Marshal(grant)
	if err != nil || bytes.Contains(data, secret.Bytes()) {
		t.Fatalf("secret leaked into grant: error=%v", err)
	}
	var restored Grant
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Authorize("task-1", "gen-1", Receive, secret.Bytes()); err != nil {
		t.Fatalf("restored grant: %v", err)
	}
	if err := restored.Authorize("task-1", "gen-1", SendReply, secret.Bytes()); !errors.Is(
		err,
		ErrDenied,
	) {
		t.Fatalf("caller mutated allowed operations: %v", err)
	}
	for _, format := range []string{"%v", "%s", "%q", "%#v"} {
		if bytes.Contains([]byte(fmt.Sprintf(format, secret)), secret.Bytes()) {
			t.Fatal("formatted secret was not redacted")
		}
	}
	copy := secret.Bytes()
	copy[0] ^= 1
	if bytes.Equal(copy, secret.Bytes()) {
		t.Fatal("secret bytes alias private storage")
	}
	_, other, err := Issue("task-1", "gen-1", []Operation{Receive})
	if err != nil || bytes.Equal(secret.Bytes(), other.Bytes()) {
		t.Fatalf("secret was reused: %v", err)
	}
}

func TestGrantRejectsInvalidBindings(t *testing.T) {
	for _, trial := range []struct {
		task       string
		generation string
		operations []Operation
	}{
		{"../task", "gen-1", []Operation{Receive}},
		{"task-1", "", []Operation{Receive}},
		{"task-1", "gen-1", nil},
		{"task-1", "gen-1", []Operation{Receive, Receive}},
		{"task-1", "gen-1", []Operation{Operation("stop")}},
	} {
		if _, _, err := Issue(trial.task, trial.generation, trial.operations); err == nil {
			t.Fatalf("invalid grant accepted: %+v", trial)
		}
	}
	if err := (Grant{}).Authorize("task-1", "gen-1", Receive, nil); !errors.Is(err, ErrDenied) {
		t.Fatalf("empty grant authorized: %v", err)
	}
}
