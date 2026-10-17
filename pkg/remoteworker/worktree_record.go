package remoteworker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
)

var ErrWorktreeOwnership = errors.New(
	"remote worktree ownership is uncertain; retain the lease and inspect its journal",
)

// WorktreeRequest identifies a physical lease independently of worker generations.
type WorktreeRequest struct {
	Task       string                `json:"task"`
	Location   taskstate.RemoteLease `json:"location"`
	Holder     string                `json:"holder"`
	Branch     string                `json:"branch"`
	BaseCommit string                `json:"base_commit"`
}

func worktreeOwnershipKey(request WorktreeRequest) string {
	digest := sha256.Sum256(
		[]byte(request.Location.PrimaryRoot + "\x00" + request.Task + "\x00" + request.Holder),
	)
	return hex.EncodeToString(digest[:])
}

func (request WorktreeRequest) Validate() error {
	if _, err := taskmeta.ParseTaskID(request.Task); err != nil {
		return ErrWorktreeOwnership
	}
	if request.Location == (taskstate.RemoteLease{}) || request.Location.Validate() != nil ||
		request.Location.Backend != "" {
		return ErrWorktreeOwnership
	}
	for _, value := range []string{request.Holder, request.Branch} {
		if value == "" || len(value) > 512 || !utf8.ValidString(value) ||
			strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return ErrWorktreeOwnership
		}
	}
	if strings.HasPrefix(request.Branch, "-") || request.Branch == "HEAD" {
		return ErrWorktreeOwnership
	}
	commit, err := hex.DecodeString(request.BaseCommit)
	if err != nil || (len(commit) != 20 && len(commit) != 32) ||
		strings.ToLower(request.BaseCommit) != request.BaseCommit {
		return ErrWorktreeOwnership
	}
	return nil
}

type worktreePhase string

const (
	worktreeReserved  worktreePhase = "reserved"
	worktreeAcquiring worktreePhase = "acquiring"
	worktreeAcquired  worktreePhase = "acquired"
	worktreeReady     worktreePhase = "ready"
	worktreeReturning worktreePhase = "returning"
	worktreeReturned  worktreePhase = "returned"
)

type worktreeRecord struct {
	Format      int                          `json:"format"`
	Request     WorktreeRequest              `json:"request"`
	Backend     taskstate.RemoteLeaseBackend `json:"backend"`
	Phase       worktreePhase                `json:"phase"`
	Lease       taskstate.WorktreeLease      `json:"lease"`
	ReturnProof WorktreeReturnProof          `json:"return_proof,omitzero"`
}

func (record worktreeRecord) validate() error {
	if record.Format != 1 || record.Request.Validate() != nil ||
		(record.Backend != taskstate.RemoteGit && record.Backend != taskstate.RemoteTreehouse) {
		return ErrWorktreeOwnership
	}
	if record.Phase == worktreeReturning || record.Phase == worktreeReturned {
		if record.ReturnProof.validate(record.Request) != nil {
			return ErrWorktreeOwnership
		}
	} else if record.ReturnProof != (WorktreeReturnProof{}) {
		return ErrWorktreeOwnership
	}
	switch record.Phase {
	case worktreeReserved, worktreeAcquiring:
		if record.Lease != (taskstate.WorktreeLease{}) {
			return ErrWorktreeOwnership
		}
		return nil
	case worktreeAcquired, worktreeReady, worktreeReturning, worktreeReturned:
	default:
		return ErrWorktreeOwnership
	}
	request, lease := record.Request, record.Lease
	location := request.Location
	location.Backend = record.Backend
	if lease.Remote != location || lease.Project != location.ProjectPath() ||
		lease.Holder != request.Holder ||
		lease.Branch != request.Branch ||
		lease.BaseCommit != request.BaseCommit {
		return ErrWorktreeOwnership
	}
	if lease.ID == "" || len(lease.ID) > 512 || !utf8.ValidString(lease.ID) ||
		strings.IndexFunc(lease.ID, unicode.IsControl) >= 0 {
		return ErrWorktreeOwnership
	}
	if _, err := taskmeta.ParseAbsolutePath(lease.Path); err != nil ||
		!strings.HasPrefix(lease.Path, location.PoolRoot()+string(filepath.Separator)) {
		return ErrWorktreeOwnership
	}
	expected := taskstate.LeaseHeld
	if record.Phase == worktreeReturned {
		expected = taskstate.LeaseReturned
	}
	if lease.State != expected {
		return ErrWorktreeOwnership
	}
	return nil
}

func nextWorktreePhase(previous, next worktreePhase) bool {
	if previous == next {
		return true
	}
	phases := []worktreePhase{
		worktreeReserved,
		worktreeAcquiring,
		worktreeAcquired,
		worktreeReady,
		worktreeReturning,
		worktreeReturned,
	}
	for index := range len(phases) - 1 {
		if previous == phases[index] && next == phases[index+1] {
			return true
		}
	}
	return false
}
