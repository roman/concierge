package taskops

import (
	"context"

	"github.com/roman/concierge/pkg/taskstate"
)

// RemoteLeases keeps native lease operations outside the primary's local pool.
type RemoteLeases interface {
	Return(
		context.Context,
		taskstate.Task,
		string,
		taskstate.WorktreeLease,
	) (taskstate.WorktreeLease, error)
	InspectAbandoned(
		context.Context,
		string,
		taskstate.LaunchAbandonment,
	) (taskstate.WorktreeLease, error)
}

func (service *Service) WithRemoteLeases(leases RemoteLeases) *Service {
	configured := *service
	configured.remoteLeases = leases
	return &configured
}
