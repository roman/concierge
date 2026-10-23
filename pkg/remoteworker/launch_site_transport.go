package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

func DecodeLaunchSiteRequest(input io.Reader) (LaunchSiteRequest, error) {
	var request LaunchSiteRequest
	if decodeSetupRequest(input, &request) != nil {
		return LaunchSiteRequest{}, errors.New("remote launch request decoding failed")
	}
	if request.Validate() != nil {
		return LaunchSiteRequest{}, errors.New("remote launch request validation failed")
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
		return LaunchSite{}, fmt.Errorf("%w: invalid runner or machine profile", ErrLaunchTransport)
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
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return LaunchSite{}, transport.launchFailure(
			fmt.Errorf("%w: %w", ErrLaunchTimeout, context.DeadlineExceeded), diagnostics)
	}
	if ctx.Err() != nil {
		return LaunchSite{}, transport.launchFailure(
			fmt.Errorf("%w: %w", ErrLaunchTransport, ctx.Err()), diagnostics)
	}
	var failure launchFailure
	if decodeSetupRequest(bytes.NewReader(output), &failure) == nil && failure.Stage != "" {
		return LaunchSite{}, transport.launchFailure(failure.cause(),
			append(append([]byte(failure.Diagnostics), '\n'), diagnostics...))
	}
	if err != nil {
		return LaunchSite{}, transport.launchFailure(
			fmt.Errorf("%w: %w", ErrLaunchTransport, err),
			diagnostics,
		)
	}
	if len(diagnostics) != 0 {
		return LaunchSite{}, transport.launchFailure(ErrLaunchStderr, diagnostics)
	}
	var site LaunchSite
	if decodeSetupRequest(bytes.NewReader(output), &site) != nil {
		return LaunchSite{}, ErrLaunchDecode
	}
	if site.Validate(request) != nil {
		return LaunchSite{}, ErrLaunchValidation
	}
	return site, nil
}
