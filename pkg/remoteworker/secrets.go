package remoteworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrSecretOperationUnconfirmed = errors.New(
	"remote secret operation is unconfirmed; retain the generation and verifier",
)

type SecretTransport struct{ Runner sshcommand.Runner }

func (transport SecretTransport) Install(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	secret workeraccess.Secret,
) error {
	data := secret.Bytes()
	if len(data) != 43 {
		return workeraccess.ErrPrivateSecret
	}
	return transport.perform(ctx, profile, tools, "install", bytes.NewReader(data))
}

func (transport SecretTransport) Remove(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	verifier string,
) error {
	decoded, err := hex.DecodeString(verifier)
	if err != nil || len(decoded) != sha256.Size {
		return workeraccess.ErrSecretConflict
	}
	return transport.perform(ctx, profile, tools, "remove", nil, "--verifier", verifier)
}

func (transport SecretTransport) perform(
	ctx context.Context,
	profile herdr.SavedMachine,
	tools Tools,
	operation string,
	input io.Reader,
	extra ...string,
) error {
	if transport.Runner == nil || !profile.Enabled || profile.ID == "" {
		return errors.New(
			"remote secret transport requires an enabled saved machine and SSH runner",
		)
	}
	if err := tools.Validate(); err != nil {
		return err
	}
	command := append(
		[]string{tools.Concierge, "worker-secret", operation, "--directory", tools.TaskDirectory},
		extra...)
	arguments, err := sshcommand.TrustedBatchArguments(profile.Target, command...)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, stderr, err := transport.Runner.Run(ctx, arguments, input)
	status := "status: installed"
	if operation == "remove" {
		status = "status: removed"
	}
	if err == nil && ctx.Err() == nil && len(stderr) == 0 &&
		strings.TrimSpace(string(stdout)) == status {
		return nil
	}
	for _, known := range []error{workeraccess.ErrPrivateSecret, workeraccess.ErrSecretConflict, workeraccess.ErrSecretUnconfirmed} {
		encoded, _ := json.Marshal(known.Error())
		if strings.Contains(string(stdout), "error: "+string(encoded)+"\n") {
			return known
		}
	}
	return ErrSecretOperationUnconfirmed
}
