package taskops

import (
	"fmt"
	"path"

	"github.com/roman/concierge/pkg/taskmeta"
)

type remoteLaunchEnvironment struct {
	Task       string
	Generation string
	Directory  string
}

func (environment remoteLaunchEnvironment) variables() (map[string]string, error) {
	if _, err := taskmeta.ParseTaskID(environment.Task); err != nil {
		return nil, err
	}
	if err := taskmeta.ValidateSpawnGeneration(environment.Generation); err != nil {
		return nil, err
	}
	if _, err := taskmeta.ParseAbsolutePath(environment.Directory); err != nil ||
		environment.Directory == "/" {
		return nil, fmt.Errorf("remote launch requires an absolute private task directory")
	}
	return map[string]string{
		"CONCIERGE_SPAWN_GENERATION":   environment.Generation,
		"CONCIERGE_WORKER_TASK":        environment.Task,
		"CONCIERGE_WORKER_SOCKET":      path.Join(environment.Directory, "worker.sock"),
		"CONCIERGE_WORKER_SECRET_FILE": path.Join(environment.Directory, "secret"),
	}, nil
}

func remoteNativeLaunchArguments(
	harness string,
	arguments []string,
	protocol string,
	environment remoteLaunchEnvironment,
) ([]string, error) {
	variables, err := environment.variables()
	if err != nil {
		return nil, err
	}
	launched, err := nativeLaunchWithEnvironment(harness, arguments, protocol, variables)
	if err != nil {
		return nil, err
	}
	// The retained reply and secret files live outside the repository worktree.
	return append([]string{"--add-dir", environment.Directory}, launched...), nil
}
