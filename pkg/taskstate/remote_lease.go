package taskstate

import (
	"encoding/hex"
	"errors"
	"path"
	"strings"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskmeta"
)

type RemoteLeaseBackend string

const (
	RemoteTreehouse RemoteLeaseBackend = "treehouse"
	RemoteGit       RemoteLeaseBackend = "git"
)

// RemoteLease keeps cleanup routing after the worker record disappears.
// Its primary paths are metadata and must never be opened on the remote.
type RemoteLease struct {
	Machine        string                    `json:"machine"`
	Target         string                    `json:"target"`
	PrimaryRoot    string                    `json:"primary_root"`
	PrimaryProject string                    `json:"primary_project"`
	Base           string                    `json:"base"`
	Repository     string                    `json:"repository"`
	ClientCommit   string                    `json:"client_commit"`
	Backend        RemoteLeaseBackend        `json:"backend"`
	Authentication sshcommand.Authentication `json:"authentication,omitempty"`
}

func (remote RemoteLease) ProjectPath() string {
	return path.Join(remote.Base, "projects", remote.Repository)
}

func (remote RemoteLease) PoolRoot() string { return path.Join(remote.Base, "worktrees") }

func (remote RemoteLease) ClientPath() string {
	return path.Join(remote.Base, "clients", remote.ClientCommit, "bin", "concierge")
}

func (remote RemoteLease) Validate() error {
	if remote == (RemoteLease{}) {
		return nil
	}
	if err := remote.Authentication.Validate(); err != nil {
		return err
	}
	if _, err := herdr.ParseMachineTarget(remote.Machine, "pane"); err != nil {
		return ErrInvalidTask
	}
	if !sshcommand.ValidTarget(remote.Target) || remote.Base == "/" || remote.Repository == "" ||
		remote.Repository == "." ||
		remote.Repository == ".." ||
		path.Base(remote.Repository) != remote.Repository ||
		!singleLine(remote.Repository) {
		return errors.New("remote lease requires a saved target and repository inside its base")
	}
	for _, directory := range []string{remote.PrimaryRoot, remote.PrimaryProject, remote.Base} {
		if _, err := taskmeta.ParseAbsolutePath(directory); err != nil {
			return err
		}
	}
	commit, err := hex.DecodeString(remote.ClientCommit)
	if err != nil || len(commit) != 20 ||
		strings.ToLower(remote.ClientCommit) != remote.ClientCommit {
		return errors.New("remote lease requires an immutable client commit")
	}
	if remote.Backend != "" && remote.Backend != RemoteTreehouse && remote.Backend != RemoteGit {
		return errors.New("remote lease has an unsupported worktree backend")
	}
	return nil
}

func recordUsesRemote(selected record) bool {
	if worker := selected.Worker; worker != nil &&
		(worker.Machine != "" || worker.Lease.Remote != (RemoteLease{})) {
		return true
	}
	if intent := selected.LaunchIntent; intent != nil &&
		(intent.Machine != "" || intent.Remote != (RemoteLease{}) || intent.Lease.Remote != (RemoteLease{})) {
		return true
	}
	if selected.Receipt != nil && selected.Receipt.Lease.Remote != (RemoteLease{}) {
		return true
	}
	for _, receipt := range selected.PriorReceipts {
		if receipt.Lease.Remote != (RemoteLease{}) {
			return true
		}
	}
	for _, abandoned := range selected.AbandonedLaunches {
		if abandoned.Remote != (RemoteLease{}) || abandoned.Lease.Remote != (RemoteLease{}) {
			return true
		}
	}
	return false
}
