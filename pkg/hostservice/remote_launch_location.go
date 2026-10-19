package hostservice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
)

// Retries retain the captured client even after machine preparation publishes a
// newer client. The existing launch directory and secret belong to that version.
func (server *Server) resolveRemoteLaunchLocation(
	ctx context.Context,
	machineID, project, repository string,
	current *taskstate.LaunchIntent,
) (herdr.SavedMachine, taskstate.RemoteLease, error) {
	var empty taskstate.RemoteLease
	root, err := os.OpenRoot(server.config.Root)
	if err != nil {
		return herdr.SavedMachine{}, empty, err
	}
	defer root.Close()
	if err := stateversion.RequireMinimum(root, 3); err != nil {
		return herdr.SavedMachine{}, empty, err
	}
	machines := server.preparationMachines
	if machines == nil {
		machines = herdr.MachineCatalog{
			Runner: herdr.NewClient(server.config.HerdrExecutable).Runner,
		}
	}
	profile, err := machines.Resolve(ctx, machineID)
	if err != nil {
		return profile, empty, err
	}
	if machineID != profile.ID || !profile.Enabled || profile.Session == "" {
		return profile, empty, errors.New("remote launch requires the enabled saved machine ID")
	}
	configuration, err := readMachineConfiguration(server.config.Root, profile)
	if err != nil {
		return profile, empty, err
	}
	configuration, err = server.resolveMachineBase(ctx, profile, configuration)
	if err != nil {
		return profile, empty, err
	}
	origin := ""
	for _, declared := range configuration.Repositories {
		if declared.Name == repository {
			origin = declared.URL
		}
	}
	if origin == "" {
		return profile, empty, errors.New(
			"declare the task repository in the machine configuration before launch",
		)
	}
	if err := server.checkRemoteLaunchOrigin(ctx, project, origin); err != nil {
		return profile, empty, err
	}
	location := taskstate.RemoteLease{
		Machine: profile.ID, Target: profile.Target,
		PrimaryRoot: server.config.Root, PrimaryProject: project,
		Base: configuration.Base, Repository: repository,
		Authentication: configuration.Authentication,
	}
	if current != nil {
		location.ClientCommit = current.Remote.ClientCommit
		if current.Machine != profile.ID || current.Session != profile.Session ||
			current.Project != location.ProjectPath() || current.Remote != location {
			return profile, empty, errors.New(
				"remote launch destination differs from the recorded intent",
			)
		}
	} else {
		runner := server.preparationSSH
		if runner == nil {
			runner = sshcommand.SystemRunner{}
		}
		client, err := (machineprep.Preparer{Runner: runner}).ReadClient(ctx, profile, configuration)
		if err != nil {
			return profile, empty, err
		}
		location.ClientCommit = client.Commit
	}
	return profile, location, location.Validate()
}

func (server *Server) checkRemoteLaunchOrigin(ctx context.Context, project, origin string) error {
	if _, err := taskmeta.ParseAbsolutePath(project); err != nil {
		return err
	}
	if _, err := taskmeta.ParseAbsolutePath(server.config.GitExecutable); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, server.config.GitExecutable,
		"-C", project, "config", "--get", "remote.origin.url")
	command.Env = sshcommand.ConnectionEnvironment(os.Environ())
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != origin {
		return errors.New("primary project origin differs from the declared task repository")
	}
	return nil
}
