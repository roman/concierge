package remoteworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
)

var ErrLaunchDirectoryOwnership = errors.New(
	"remote launch directory ownership is uncertain; retain its generation and files",
)

var ErrLaunchSocketPath = errors.New(
	"remote socket path is too long; configure a shorter machine base path",
)

type LaunchSiteRequest struct {
	Worktree   WorktreeRequest         `json:"worktree"`
	Lease      taskstate.WorktreeLease `json:"lease"`
	Generation string                  `json:"generation"`
}

type LaunchSite struct {
	Lease       taskstate.WorktreeLease   `json:"lease"`
	Tools       Tools                     `json:"tools"`
	Environment herdr.WorktreeEnvironment `json:"environment"`
}

func (request LaunchSiteRequest) Validate() error {
	if taskmeta.ValidateSpawnGeneration(request.Generation) != nil {
		return ErrWorktreeOwnership
	}
	record := worktreeRecord{
		Format:  1,
		Request: request.Worktree,
		Backend: request.Lease.Remote.Backend,
		Phase:   worktreeReady,
		Lease:   request.Lease,
	}
	return record.validate()
}

func (request LaunchSiteRequest) Tools() (Tools, error) {
	if err := request.Validate(); err != nil {
		return Tools{}, err
	}
	return ToolsForGeneration(request.Worktree.Location, request.Worktree.Task, request.Generation)
}

// ToolsForGeneration fixes native paths before a physical lease is captured.
func ToolsForGeneration(location taskstate.RemoteLease, task, generation string) (Tools, error) {
	if location == (taskstate.RemoteLease{}) || location.Validate() != nil ||
		taskmeta.ValidateSpawnGeneration(generation) != nil {
		return Tools{}, ErrWorktreeOwnership
	}
	if _, err := taskmeta.ParseTaskID(task); err != nil {
		return Tools{}, ErrWorktreeOwnership
	}
	digest := sha256.Sum256(
		[]byte(
			location.PrimaryRoot + "\x00" + task + "\x00" + generation,
		),
	)
	// A 128-bit directory identity leaves room for Darwin's Unix socket path limit.
	name := hex.EncodeToString(digest[:16])
	tools := Tools{
		Concierge:     location.ClientPath(),
		TaskDirectory: filepath.Join(location.Base, "tasks", name),
	}
	if len(filepath.Join(tools.TaskDirectory, "worker.sock")) > 103 {
		return Tools{}, ErrLaunchSocketPath
	}
	return tools, tools.Validate()
}

func (site LaunchSite) Validate(request LaunchSiteRequest) error {
	tools, err := request.Tools()
	if err != nil {
		return err
	}
	if site.Lease != request.Lease || site.Tools != tools {
		return ErrLaunchDirectoryOwnership
	}
	switch site.Environment {
	case herdr.PlainWorktree, herdr.DirenvWorktree, herdr.NixWorktree:
		return nil
	default:
		return ErrLaunchDirectoryOwnership
	}
}

// PrepareLaunchSite runs on the remote before native startup. Credentials remain
// absent until the primary binds the worker and installs its active grant.
func (manager Worktrees) PrepareLaunchSite(
	ctx context.Context,
	request LaunchSiteRequest,
) (LaunchSite, error) {
	tools, err := request.Tools()
	if err != nil {
		return LaunchSite{}, err
	}
	if _, err := manager.PrepareProject(ctx, request.Worktree.Location); err != nil {
		return LaunchSite{}, err
	}
	var result LaunchSite
	err = withWorktreeJournal(ctx, request.Worktree, func(journal *worktreeJournal) error {
		record, err := journal.read()
		if err != nil {
			return err
		}
		if record == nil || record.Phase != worktreeReady || record.Lease != request.Lease {
			return ErrWorktreeOwnership
		}
		if err := manager.verifyLease(ctx, *record); err != nil {
			return err
		}
		environment, err := herdr.DetectWorktreeEnvironment(ctx, record.Lease.Path)
		if err != nil {
			return err
		}
		if err := prepareLaunchDirectory(request, tools); err != nil {
			return err
		}
		result = LaunchSite{Lease: record.Lease, Tools: tools, Environment: environment}
		return nil
	})
	return result, err
}
