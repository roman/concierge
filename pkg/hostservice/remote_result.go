package hostservice

import (
	"context"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
)

type hostRemoteResultExporter struct{ server *Server }

func (exporter hostRemoteResultExporter) Export(
	ctx context.Context,
	expected taskstate.Task,
	selected string,
) (*worktree.CommitBundle, error) {
	worker := expected.Snapshot().Worker
	if worker == nil {
		return nil, taskstate.ErrInvalidTransition
	}
	var bundle *worktree.CommitBundle
	_, err := exporter.server.store.WithTask(
		ctx,
		expected.ID(),
		func(locked *taskstate.LockedTask) error {
			current := locked.Current().Snapshot()
			bound := current.Worker
			if current.State != taskstate.InFlight || current.Completion != nil ||
				current.LaunchIntent != nil ||
				bound == nil {
				return taskstate.ErrInvalidTransition
			}
			if bound.Generation != worker.Generation || bound.Lease != worker.Lease ||
				bound.RemoteResult.Commit != selected || current.Definition.Repo != bound.Lease.Remote.Repository {
				return taskstate.ErrInvalidTransition
			}
			profile, err := (hostRemoteRuntime{server: exporter.server}).profile(ctx, *bound)
			if err != nil {
				return err
			}
			configuration, err := readMachineConfiguration(exporter.server.config.Root, profile)
			if err != nil {
				return err
			}
			origin := ""
			for _, repository := range configuration.Repositories {
				if repository.Name == current.Definition.Repo {
					origin = repository.URL
				}
			}
			if origin == "" {
				return remoteworker.ErrWorktreeOwnership
			}
			if err := exporter.server.checkRemoteLaunchOrigin(ctx, bound.Lease.Remote.PrimaryProject, origin); err != nil {
				return err
			}
			location := bound.Lease.Remote
			location.Backend = ""
			request := remoteworker.WorktreeResultRequest{Worktree: remoteworker.WorktreeRequest{
				Task: expected.ID(), Location: location, Holder: bound.Lease.Holder,
				Branch: bound.Lease.Branch, BaseCommit: bound.Lease.BaseCommit}, Lease: bound.Lease, Commit: selected}
			if err := locked.CheckCurrent(); err != nil {
				return err
			}
			transport := (hostRemoteLeases{server: exporter.server}).transport()
			transport.MaxResultBytes = configuration.MaxResultBytes
			result, err := transport.ExportResult(ctx, profile, request)
			if err != nil {
				return err
			}
			if err := locked.CheckCurrent(); err != nil {
				result.Bundle.Close()
				return err
			}
			bundle = result.Bundle
			return nil
		},
	)
	if err != nil {
		if bundle != nil {
			bundle.Close()
		}
		return nil, err
	}
	return bundle, nil
}
