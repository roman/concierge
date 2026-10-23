package remoteworker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type WorktreeReturnOutcome string

const (
	WorktreeUnchanged WorktreeReturnOutcome = "unchanged"
	WorktreeLanded    WorktreeReturnOutcome = "landed"
	WorktreeScout     WorktreeReturnOutcome = "accepted_scout"
	WorktreeAbandoned WorktreeReturnOutcome = "abandoned"
)

// Landed and abandoned outcomes come from the trusted primary. The remote
// cannot inspect the primary's acceptance record or its local landing branch.
type WorktreeReturnProof struct {
	Outcome WorktreeReturnOutcome `json:"outcome"`
	Commit  string                `json:"commit"`
}

func (proof WorktreeReturnProof) validate(request WorktreeRequest) error {
	commit, err := hex.DecodeString(proof.Commit)
	if err != nil || (len(commit) != 20 && len(commit) != 32) ||
		strings.ToLower(proof.Commit) != proof.Commit {
		return ErrWorktreeOwnership
	}
	switch proof.Outcome {
	case WorktreeUnchanged:
		if proof.Commit != request.BaseCommit {
			return ErrWorktreeOwnership
		}
	case WorktreeLanded, WorktreeScout, WorktreeAbandoned:
	default:
		return ErrWorktreeOwnership
	}
	return nil
}

type WorktreeReturnRequest struct {
	Worktree WorktreeRequest         `json:"worktree"`
	Lease    taskstate.WorktreeLease `json:"lease"`
	Proof    WorktreeReturnProof     `json:"proof"`
	// Only the trusted primary supplies operator-owned project settings.
	ReturnIgnoredPaths []string `json:"return_ignored_paths,omitempty"`
}

// WorktreeReturnResponse preserves removal diagnostics on partial failures.
type WorktreeReturnResponse struct {
	taskstate.WorktreeLease
	RemovedPaths []string `json:"removed_paths,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func validateRemovedPaths(directory string, paths []string) error {
	encoded, err := json.Marshal(paths)
	if err != nil || len(encoded) > 24<<10 {
		return ErrWorktreeOwnership
	}
	for _, path := range paths {
		if filepath.Clean(path) != path || !strings.HasPrefix(path, directory+"/") {
			return ErrWorktreeOwnership
		}
	}
	return nil
}

func (request WorktreeReturnRequest) Validate() error {
	record := worktreeRecord{Format: 1, Request: request.Worktree,
		Backend: request.Lease.Remote.Backend, Phase: worktreeReady, Lease: request.Lease}
	if record.validate() != nil || request.Proof.validate(request.Worktree) != nil ||
		worktree.ValidateReturnIgnoredPaths(request.ReturnIgnoredPaths) != nil {
		return ErrWorktreeOwnership
	}
	return nil
}

func (manager Worktrees) Return(
	ctx context.Context,
	request WorktreeReturnRequest,
) (taskstate.WorktreeLease, error) {
	if request.Validate() != nil || !filepath.IsAbs(manager.Git.Executable) {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	manager.Git.ReturnIgnoredPaths = request.ReturnIgnoredPaths
	var returned taskstate.WorktreeLease
	err := withWorktreeJournal(
		ctx,
		request.Worktree,
		func(journal *worktreeJournal) (returnErr error) {
			record, err := journal.read()
			if err != nil || record == nil {
				return ErrWorktreeOwnership
			}
			captured := record.Lease
			captured.State = taskstate.LeaseHeld
			if captured != request.Lease ||
				(record.ReturnProof != (WorktreeReturnProof{}) && record.ReturnProof != request.Proof) {
				return ErrWorktreeOwnership
			}
			report := manager.Git.RemovedPath
			for _, path := range record.RemovedPaths {
				if report != nil {
					report(path)
				}
			}
			if record.Phase == worktreeReturned {
				returned = record.Lease
				return nil
			}
			if record.Phase != worktreeReady && record.Phase != worktreeReturning {
				return ErrWorktreeOwnership
			}
			previousPaths := len(record.RemovedPaths)
			manager.Git.RemovedPath = func(path string) {
				if !slices.Contains(record.RemovedPaths, path) {
					record.RemovedPaths = append(record.RemovedPaths, path)
					if report != nil {
						report(path)
					}
				}
			}
			defer func() {
				if len(record.RemovedPaths) != previousPaths {
					returnErr = errors.Join(returnErr, journal.save(*record))
				}
			}()
			if _, err := manager.PrepareProject(ctx, request.Worktree.Location); err != nil {
				return err
			}
			if record.Phase == worktreeReady {
				if err := manager.verifyReturnable(ctx, *record, request.Proof); err != nil {
					return err
				}
				record.Phase, record.ReturnProof = worktreeReturning, request.Proof
				if err := journal.save(*record); err != nil {
					return err
				}
			}
			if record.Backend == taskstate.RemoteTreehouse {
				pool := worktree.PoolClient{
					Executable: manager.Treehouse,
					Root:       record.Lease.Remote.PoolRoot(),
				}
				if _, found, err := pool.Find(ctx, record.Lease.Project, record.Lease.Holder); err != nil {
					return err
				} else if found {
					if err := manager.verifyReturnable(ctx, *record, request.Proof); err != nil {
						return err
					}
				}
				if err := pool.Return(ctx, record.Lease.Project, worktree.Lease{
					ID: record.Lease.ID, Holder: record.Lease.Holder, Path: record.Lease.Path,
				}); err != nil {
					return err
				}
			} else if err := manager.removeReturnedGitWorktree(ctx, *record); err != nil {
				return err
			}
			record.Phase, record.Lease.State = worktreeReturned, taskstate.LeaseReturned
			if err := journal.save(*record); err != nil {
				return err
			}
			returned = record.Lease
			return nil
		},
	)
	return returned, err
}

func (manager Worktrees) verifyReturnable(
	ctx context.Context,
	record worktreeRecord,
	proof WorktreeReturnProof,
) error {
	if err := manager.verifyLease(ctx, record); err != nil {
		return err
	}
	commit, err := manager.projectGit(ctx, record.Lease.Path, "rev-parse", "HEAD")
	if err != nil || commit != proof.Commit {
		return ErrWorktreeOwnership
	}
	if err := manager.Git.StopFsmonitor(ctx, record.Lease.Path); err != nil {
		return err
	}
	quiet := manager.CheckQuiet
	if quiet == nil {
		quiet = func(ctx context.Context, directory string) error { return checkWorktreeQuiet(ctx, "lsof", directory) }
	}
	if err := quiet(ctx, record.Lease.Path); err != nil {
		return err
	}
	if record.Backend == taskstate.RemoteTreehouse {
		pool := worktree.PoolClient{
			Executable: manager.Treehouse,
			Root:       record.Lease.Remote.PoolRoot(),
		}
		if err := pool.VerifyReturn(ctx, record.Lease.Project, worktree.Lease{
			ID: record.Lease.ID, Holder: record.Lease.Holder, Path: record.Lease.Path,
		}); err != nil {
			return err
		}
	}
	if proof.Outcome == WorktreeLanded || proof.Outcome == WorktreeScout {
		return manager.Git.CleanReturn(ctx, remoteWorktreeSpecification(record.Lease))
	}
	return manager.Git.VerifyReturn(ctx, remoteWorktreeSpecification(record.Lease))
}

func (manager Worktrees) removeReturnedGitWorktree(
	ctx context.Context,
	record worktreeRecord,
) error {
	lease := record.Lease
	if _, err := os.Lstat(lease.Path); err == nil {
		if err := manager.verifyReturnable(ctx, record, record.ReturnProof); err != nil {
			return err
		}
		if _, err := manager.projectGit(ctx, lease.Project, "worktree", "remove", "--", lease.Path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrWorktreeOwnership
	}
	if _, err := os.Lstat(lease.Path); !errors.Is(err, os.ErrNotExist) {
		return ErrWorktreeOwnership
	}
	entries, err := manager.projectGit(ctx, lease.Project, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	for _, field := range strings.Split(entries, "\x00") {
		if field == "worktree "+lease.Path {
			return ErrWorktreeOwnership
		}
	}
	commit, err := manager.projectGit(
		ctx,
		lease.Project,
		"rev-parse",
		"--verify",
		"refs/heads/"+lease.Branch+"^{commit}",
	)
	if err != nil || commit != record.ReturnProof.Commit {
		return ErrWorktreeOwnership
	}
	return nil
}
