// Package workeraccess restricts remote clients to one task generation.
package workeraccess

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/roman/concierge/pkg/taskmeta"
)

type Operation string

const (
	ListPending   Operation = "pending"
	ReadBootstrap Operation = "bootstrap-read"
	AckBootstrap  Operation = "bootstrap-ack"
	Receive       Operation = "receive"
	ReadSettings  Operation = "settings"
	SendReply     Operation = "reply-send"
	AckAssignment Operation = "ack"
)

var ErrDenied = errors.New("worker access denied")

func Operations() []Operation {
	return []Operation{
		ListPending,
		ReadBootstrap,
		AckBootstrap,
		Receive,
		ReadSettings,
		SendReply,
		AckAssignment,
	}
}

// Grant stores a verifier, never the secret delivered to the remote task.
type Grant struct {
	Task       string      `json:"task"`
	Generation string      `json:"generation"`
	Operations []Operation `json:"operations"`
	Verifier   string      `json:"verifier"`
	Revoked    bool        `json:"revoked"`
}

// Secret must be explicitly copied for stdin delivery. Formatting redacts it.
type Secret struct{ value []byte }

func (secret Secret) Bytes() []byte { return slices.Clone(secret.value) }

func (secret Secret) Format(
	output fmt.State,
	_ rune,
) {
	_, _ = io.WriteString(output, "[redacted]")
}

func (secret Secret) MarshalJSON() ([]byte, error) {
	return nil, errors.New("worker secrets cannot be serialized")
}

func Issue(task, generation string, operations []Operation) (Grant, Secret, error) {
	grant := Grant{Task: task, Generation: generation, Operations: slices.Clone(operations)}
	if err := grant.validateBinding(); err != nil {
		return Grant{}, Secret{}, err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Grant{}, Secret{}, err
	}
	secret := Secret{value: []byte(base64.RawURLEncoding.EncodeToString(random))}
	verifier := sha256.Sum256(secret.value)
	grant.Verifier = hex.EncodeToString(verifier[:])
	return grant, secret, nil
}

func (grant Grant) validateBinding() error {
	if _, err := taskmeta.ParseTaskID(grant.Task); err != nil {
		return ErrDenied
	}
	if taskmeta.ValidateSpawnGeneration(grant.Generation) != nil || len(grant.Operations) == 0 {
		return ErrDenied
	}
	allowed := Operations()
	for index, operation := range grant.Operations {
		if !slices.Contains(allowed, operation) ||
			slices.Contains(grant.Operations[:index], operation) {
			return ErrDenied
		}
	}
	return nil
}

func (grant Grant) Validate() error {
	if err := grant.validateBinding(); err != nil {
		return err
	}
	verifier, err := hex.DecodeString(grant.Verifier)
	if err != nil || len(verifier) != sha256.Size ||
		grant.Verifier != hex.EncodeToString(verifier) {
		return ErrDenied
	}
	return nil
}

func (grant Grant) Authorize(task, generation string, operation Operation, secret []byte) error {
	expected := [sha256.Size]byte{}
	decoded, _ := hex.DecodeString(grant.Verifier)
	copy(expected[:], decoded)
	actual := sha256.Sum256(secret)
	matched := subtle.ConstantTimeCompare(expected[:], actual[:])
	if matched != 1 || grant.Validate() != nil || grant.Revoked ||
		grant.Task != task || grant.Generation != generation ||
		!slices.Contains(grant.Operations, operation) {
		return ErrDenied
	}
	return nil
}
