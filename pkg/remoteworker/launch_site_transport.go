package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

func DecodeLaunchSiteRequest(input io.Reader) (LaunchSiteRequest, error) {
	var request LaunchSiteRequest
	if decodeSetupRequest(input, &request) != nil || request.Validate() != nil {
		return LaunchSiteRequest{}, ErrLaunchDirectoryOwnership
	}
	return request, nil
}

func (transport WorktreeTransport) PrepareLaunchSite(
	ctx context.Context,
	profile herdr.SavedMachine,
	request LaunchSiteRequest,
) (LaunchSite, error) {
	if _, err := request.Tools(); err != nil {
		return LaunchSite{}, err
	}
	location := request.Worktree.Location
	if transport.Runner == nil || !profile.Enabled || profile.ID != location.Machine ||
		profile.Target != location.Target {
		return LaunchSite{}, ErrLaunchDirectoryOwnership
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		profile.Target,
		location.Authentication,
		location.ClientPath(),
		"worker-worktree",
		"prepare-launch",
	)
	if err != nil {
		return LaunchSite{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return LaunchSite{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, diagnostics, err := transport.Runner.Run(ctx, arguments, bytes.NewReader(data))
	if err != nil || ctx.Err() != nil || len(diagnostics) != 0 {
		return LaunchSite{}, ErrLaunchDirectoryOwnership
	}
	var site LaunchSite
	if decodeSetupRequest(bytes.NewReader(output), &site) != nil || site.Validate(request) != nil {
		return LaunchSite{}, ErrLaunchDirectoryOwnership
	}
	return site, nil
}
