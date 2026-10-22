package hostservice

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/versioncheck"
)

func (server *Server) checkLaunchVersions(
	ctx context.Context,
	input LaunchRequest,
	options versioncheck.Options,
) (versioncheck.Report, error) {
	options.PrimaryCommit, options.Agent = server.config.Revision, input.Harness
	runner := server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	report := (versioncheck.Collector{Herdr: server.config.HerdrExecutable, Local: server.versionProbe, Remote: runner}).Inspect(
		ctx,
		options,
	)
	if input.Machine == "" {
		// Local launch admission needs only observed server problems. Remote
		// delegation also requires a complete comparison of both installations.
		report.Warnings = slices.DeleteFunc(
			report.Warnings,
			func(warning versioncheck.Warning) bool {
				return warning.Unconfirmed
			},
		)
	}
	if err := versioncheck.Publish(server.config.MessageRoot, server.config.Recipient, server.config.Root, input.TaskID, report); err != nil {
		return report, fmt.Errorf("could not publish component warnings before launch: %w", err)
	}
	if report.Blocked() {
		return report, errors.New(
			"Herdr reports incompatible protocols; correct the component warnings before launching",
		)
	}
	return report, nil
}
