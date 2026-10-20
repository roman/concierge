package conciergecli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/tincan/pkg/inbox"
)

const hostHelp = "concierge --root ROOT host serve; initialize the state root first and run the host outside the model sandbox"

func (command *cli) runHost(ctx context.Context, root string, output io.Writer) int {
	monitor, err := (&supervisionCommands{Interval: command.Host.Interval, RestartTolerance: command.Host.RestartTolerance, RestartWindow: command.Host.RestartWindow}).config()
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	recipient, err := inbox.ParseRecipient(command.Recipient)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	messageRoot, err := rootWithDefault(command.MessageRoot, ".tincan")
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	executable, err := resolveHerdrExecutable(command.Host.Herdr)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	conciergeExecutable, err := os.Executable()
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	tincanExecutable, err := exec.LookPath("tincan")
	if err != nil {
		return fail(output, 2, err, "install Tincan beside Concierge before starting the host")
	}
	tincanExecutable, err = filepath.Abs(tincanExecutable)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		return fail(output, 2, err, "install Git before starting the host")
	}
	gitExecutable, err = filepath.Abs(gitExecutable)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	treehouseExecutable, err := exec.LookPath("treehouse")
	if err != nil {
		return fail(output, 2, err, "install treehouse beside Concierge before starting the host")
	}
	treehouseExecutable, err = filepath.Abs(treehouseExecutable)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	recovery := hostservice.RecoveryNormal
	if command.Host.Recovery != nil {
		recovery = *command.Host.Recovery
	}
	server, err := hostservice.New(
		hostservice.Config{
			Root:                root,
			MessageRoot:         messageRoot,
			Recipient:           recipient,
			HerdrExecutable:     executable,
			GitExecutable:       gitExecutable,
			TreehouseExecutable: treehouseExecutable,
			WorkerCommands: taskops.CommandPaths{
				Concierge: conciergeExecutable,
				Tincan:    tincanExecutable,
			},
			WorkerSettings: func(repo string) (taskops.WorkerSettings, error) {
				settings, err := readProjectSettings(
					filepath.Join(root, "config", "projects.json"),
					repo,
				)
				return taskops.WorkerSettings{
					Repo:     repo,
					Merge:    settings.Merge,
					Delivery: settings.Delivery,
				}, err
			},
			Revision:    revision,
			Session:     command.Host.Session,
			Supervision: monitor,
			Recovery:    recovery,
		},
	)
	if err != nil {
		return fail(output, 2, err, hostHelp)
	}
	listener, err := server.Listen(ctx)
	if err != nil {
		return fail(output, 1, err, hostHelp)
	}
	defer listener.Close()
	if err := server.ServeReady(ctx, listener.UnixListener, func() error {
		_, err := fmt.Fprintf(output, "status: ready\nsocket: %s\n", quote(filepath.Join(root, "host.sock")))
		return err
	}); err != nil {
		return fail(output, 1, err, hostHelp)
	}

	return 0
}
