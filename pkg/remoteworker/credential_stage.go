package remoteworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrCredentialStageOwnership = errors.New(
	"primary credential staging ownership is uncertain; retain the generation and verifier",
)

type CredentialStage struct {
	guard     *stateversion.Guard
	namespace *os.Root
	root      *os.Root
	name      string
	expected  []byte
}

// OpenPrimaryCredentialStage pins temporary setup storage on the primary.
// The caller holds the task lock until setup and staging cleanup finish.
func OpenPrimaryCredentialStage(
	ctx context.Context,
	primaryRoot string,
	request LaunchSiteRequest,
) (*CredentialStage, error) {
	if request.Validate() != nil || request.Worktree.Location.PrimaryRoot != primaryRoot {
		return nil, ErrCredentialStageOwnership
	}
	expected, err := json.Marshal(launchDirectoryOwner{Format: 1, Request: request})
	if err != nil {
		return nil, err
	}
	guard, err := stateversion.AcquireCurrent(ctx, primaryRoot)
	if err != nil {
		return nil, err
	}
	stage := &CredentialStage{guard: guard, expected: expected}
	complete := false
	defer func() {
		if !complete {
			stage.Close()
		}
	}()
	root := guard.StateRoot()
	if err := stateversion.RequireMinimum(root, 3); err != nil {
		return nil, err
	}
	if err := root.Mkdir("remote-credentials", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	stage.namespace, err = openPrivateCredentialDirectory(root, "remote-credentials")
	if err != nil {
		return nil, err
	}
	if err := syncSocketDirectory(root); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(request.Worktree.Task + "\x00" + request.Generation))
	stage.name = hex.EncodeToString(digest[:])
	created := true
	if err := stage.namespace.Mkdir(stage.name, 0700); errors.Is(err, os.ErrExist) {
		created = false
	} else if err != nil {
		return nil, err
	}
	stage.root, err = openPrivateCredentialDirectory(stage.namespace, stage.name)
	if err != nil {
		return nil, err
	}
	if created {
		if err := writeLaunchDirectoryOwner(stage.root, expected); err != nil {
			return nil, err
		}
	}
	if err := stage.check(); err != nil {
		return nil, err
	}
	if err := syncSocketDirectory(stage.namespace); err != nil {
		return nil, err
	}
	complete = true
	return stage, nil
}

func openPrivateCredentialDirectory(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return nil, ErrCredentialStageOwnership
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, ErrCredentialStageOwnership
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) || !privateTunnelFile(after, os.ModeDir) {
		root.Close()
		return nil, ErrCredentialStageOwnership
	}
	return root, nil
}

func (stage *CredentialStage) check() error {
	if stage == nil || stage.guard == nil || stage.root == nil || stage.namespace == nil {
		return ErrCredentialStageOwnership
	}
	for _, binding := range []struct {
		parent *os.Root
		name   string
		opened *os.Root
	}{
		{stage.guard.StateRoot(), "remote-credentials", stage.namespace},
		{stage.namespace, stage.name, stage.root},
	} {
		selected, err := binding.parent.Lstat(binding.name)
		if err != nil || !privateTunnelFile(selected, os.ModeDir) {
			return ErrCredentialStageOwnership
		}
		opened, err := binding.opened.Stat(".")
		if err != nil || !os.SameFile(selected, opened) {
			return ErrCredentialStageOwnership
		}
	}
	if err := confirmLaunchDirectoryOwner(stage.root, stage.expected); err != nil {
		return ErrCredentialStageOwnership
	}
	return nil
}

func (stage *CredentialStage) ReadSecret() ([]byte, error) {
	if err := stage.check(); err != nil {
		return nil, err
	}
	if _, err := stage.root.Lstat("secret"); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, ErrCredentialStageOwnership
	}
	return workeraccess.ReadDirectorySecret(stage.root)
}

func (stage *CredentialStage) InstallSecret(secret workeraccess.Secret) error {
	if err := stage.check(); err != nil {
		return err
	}
	if err := workeraccess.InstallDirectorySecret(stage.root, bytes.NewReader(secret.Bytes())); err != nil {
		return err
	}
	return stage.check()
}

func (stage *CredentialStage) RemoveSecret(verifier string) error {
	if err := stage.check(); err != nil {
		return err
	}
	if err := workeraccess.RemoveDirectorySecret(stage.root, verifier); err != nil {
		return err
	}
	return stage.check()
}

func (stage *CredentialStage) Close() error {
	if stage == nil || stage.guard == nil {
		return nil
	}
	var result error
	if stage.root != nil {
		result = stage.root.Close()
		stage.root = nil
	}
	if stage.namespace != nil {
		result = errors.Join(result, stage.namespace.Close())
		stage.namespace = nil
	}
	result = errors.Join(result, stage.guard.Close())
	stage.guard = nil
	return result
}
