package remoteworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/worktree"
)

// Acquire restores a captured lease without resetting a worker's commits or edits.
// An interrupted Treehouse acquisition may only recover from its durable inventory.
func (manager Worktrees) Acquire(
	ctx context.Context,
	request WorktreeRequest,
) (taskstate.WorktreeLease, error) {
	if request.Validate() != nil ||
		(manager.Treehouse != "" && !filepath.IsAbs(manager.Treehouse)) {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	project, err := manager.PrepareProject(ctx, request.Location)
	if err != nil {
		return taskstate.WorktreeLease{}, err
	}
	specification := worktree.Specification{
		Project:    project,
		Path:       filepath.Join(request.Location.PoolRoot(), "pending"),
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
	}
	if manager.Git.Validate(ctx, specification) != nil {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	var result taskstate.WorktreeLease
	err = withWorktreeJournal(ctx, request, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		if record == nil {
			backend := taskstate.RemoteGit
			if manager.Treehouse != "" {
				backend = taskstate.RemoteTreehouse
			}
			record = &worktreeRecord{
				Format:  1,
				Request: request,
				Backend: backend,
				Phase:   worktreeReserved,
			}
			if err := journal.save(*record); err != nil {
				return err
			}
		}
		if record.Phase == worktreeReturning || record.Phase == worktreeReturned {
			return ErrWorktreeOwnership
		}
		if record.Backend == taskstate.RemoteTreehouse && manager.Treehouse == "" {
			return ErrWorktreeOwnership
		}
		if err := prepareWorktreePool(request.Location.Base); err != nil {
			return err
		}
		if record.Phase == worktreeReady {
			if err := manager.verifyLease(ctx, *record); err != nil {
				return err
			}
			if err := journal.save(*record); err != nil {
				return err
			}
			result = record.Lease
			return nil
		}
		attempt := recoverAcquisition
		if record.Phase == worktreeReserved {
			attempt = acquireNew
			record.Phase = worktreeAcquiring
			if err := journal.save(*record); err != nil {
				return err
			}
		}
		if record.Phase == worktreeAcquiring {
			lease, err := manager.acquireWorktree(ctx, *record, attempt)
			if err != nil {
				return err
			}
			record.Lease = lease
			record.Phase = worktreeAcquired
			if err := journal.save(*record); err != nil {
				return err
			}
		}
		if err := canonicalRemoteWorktree(record.Lease.Path); err != nil {
			return err
		}
		if manager.Git.PrepareLeased(ctx, remoteWorktreeSpecification(record.Lease)) != nil {
			return ErrWorktreeOwnership
		}
		if err := manager.verifyLease(ctx, *record); err != nil {
			return err
		}
		record.Phase = worktreeReady
		if err := journal.save(*record); err != nil {
			return err
		}
		result = record.Lease
		return nil
	})
	return result, err
}

type acquisitionAttempt string

const (
	acquireNew         acquisitionAttempt = "new"
	recoverAcquisition acquisitionAttempt = "recover"
)

func (manager Worktrees) acquireWorktree(
	ctx context.Context,
	record worktreeRecord,
	attempt acquisitionAttempt,
) (taskstate.WorktreeLease, error) {
	request := record.Request
	location := request.Location
	location.Backend = record.Backend
	lease := taskstate.WorktreeLease{
		Remote:     location,
		Holder:     request.Holder,
		Project:    location.ProjectPath(),
		Branch:     request.Branch,
		BaseCommit: request.BaseCommit,
		State:      taskstate.LeaseHeld,
	}
	if record.Backend == taskstate.RemoteTreehouse {
		pool := worktree.PoolClient{Executable: manager.Treehouse, Root: location.PoolRoot()}
		var captured worktree.Lease
		if attempt == acquireNew {
			var err error
			captured, err = pool.Acquire(ctx, lease.Project, request.Holder)
			if err != nil {
				return taskstate.WorktreeLease{}, ErrWorktreeOwnership
			}
		} else {
			var found bool
			var err error
			captured, found, err = pool.Find(ctx, lease.Project, request.Holder)
			if err != nil || !found {
				return taskstate.WorktreeLease{}, ErrWorktreeOwnership
			}
		}
		lease.ID, lease.Path = captured.ID, captured.Path
		return lease, nil
	}
	digest := sha256.Sum256([]byte(location.PrimaryRoot + "\x00" + request.Task))
	identity := hex.EncodeToString(digest[:])
	lease.ID = "git-" + identity
	lease.Path = filepath.Join(location.PoolRoot(), "git", identity)
	if manager.Git.Prepare(ctx, remoteWorktreeSpecification(lease)) != nil {
		return taskstate.WorktreeLease{}, ErrWorktreeOwnership
	}
	return lease, nil
}

func (manager Worktrees) verifyLease(ctx context.Context, record worktreeRecord) error {
	lease := record.Lease
	if err := canonicalRemoteWorktree(lease.Path); err != nil {
		return err
	}
	if manager.Git.VerifyIdentity(ctx, remoteWorktreeSpecification(lease)) != nil {
		return ErrWorktreeOwnership
	}
	if record.Backend == taskstate.RemoteTreehouse {
		pool := worktree.PoolClient{Executable: manager.Treehouse, Root: lease.Remote.PoolRoot()}
		captured, found, err := pool.Find(ctx, lease.Project, lease.Holder)
		if err != nil || !found ||
			captured != (worktree.Lease{Path: lease.Path, ID: lease.ID, Holder: lease.Holder}) {
			return ErrWorktreeOwnership
		}
	}
	return nil
}

func remoteWorktreeSpecification(lease taskstate.WorktreeLease) worktree.Specification {
	return worktree.Specification{
		Project:    lease.Project,
		Path:       lease.Path,
		Branch:     lease.Branch,
		BaseCommit: lease.BaseCommit,
	}
}

func canonicalRemoteWorktree(directory string) error {
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return ErrWorktreeOwnership
	}
	return nil
}

func prepareWorktreePool(base string) error {
	root, err := workeraccess.OpenPrivateDirectory(base)
	if err != nil {
		return ErrWorktreeOwnership
	}
	defer root.Close()
	if err := root.Mkdir("worktrees", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	before, err := root.Lstat("worktrees")
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return ErrWorktreeOwnership
	}
	pool, err := root.OpenRoot("worktrees")
	if err != nil {
		return ErrWorktreeOwnership
	}
	defer pool.Close()
	if after, err := pool.Stat("."); err != nil || !os.SameFile(before, after) {
		return ErrWorktreeOwnership
	}
	if err := pool.Mkdir("git", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if git, err := pool.Lstat("git"); err != nil || !privateTunnelFile(git, os.ModeDir) {
		return ErrWorktreeOwnership
	}
	return syncSocketDirectory(root)
}
