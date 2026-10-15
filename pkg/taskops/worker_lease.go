package taskops

import (
	"context"
	"fmt"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/worktree"
	"github.com/roman/tincan/pkg/inbox"
)

// WorkerTools fixes the executable dependencies used by hosted workers.
type WorkerTools struct {
	Commands CommandPaths
	Pool     worktree.PoolClient
	Git      worktree.Client
}

// WithRemovedStoreLinkReporter scopes return diagnostics to one operation.
func (service *Service) WithRemovedStoreLinkReporter(report func(string)) *Service {
	operation := *service
	operation.git.RemovedStoreLink = report
	return &operation
}

func NewWithWorkerTools(
	root string,
	recipient inbox.Recipient,
	tools WorkerTools,
) (*Service, error) {
	for _, executable := range []string{tools.Pool.Executable, tools.Git.Executable} {
		if executable != "" {
			if _, err := taskmeta.ParseAbsolutePath(executable); err != nil {
				return nil, err
			}
		}
	}
	service, err := NewWithWorkerCommands(root, recipient, tools.Commands)
	if err != nil {
		return nil, err
	}
	service.pool = tools.Pool
	service.git = tools.Git
	return service, nil
}

func checkWorktreeLease(
	ctx context.Context,
	lease taskstate.WorktreeLease,
	pool worktree.PoolClient,
) error {
	if lease == (taskstate.WorktreeLease{}) {
		return nil
	}
	current, found, err := pool.Find(ctx, lease.Project, lease.Holder)
	if err != nil {
		return err
	}
	if !found || current.ID != lease.ID || current.Path != lease.Path ||
		current.Holder != lease.Holder {
		return fmt.Errorf("%w: recorded treehouse lease changed", worktree.ErrIdentity)
	}
	return nil
}
