package conciergecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
)

func (command *cli) checkHeartbeatRuntime(ctx context.Context, root string) error {
	messageRoot, err := rootWithDefault(command.MessageRoot, ".tincan")
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	response, err := hostservice.Call(probe, filepath.Join(root, "host.sock"), hostservice.Request{
		Operation: hostservice.HostStatus,
		Status:    &hostservice.StatusRequest{},
		Expectations: &hostservice.Expectations{
			MessageRoot: messageRoot,
			Recipient:   command.Recipient,
		},
	})
	if err != nil {
		return err
	}
	if response.Outcome != hostservice.Completed {
		return errors.New(response.Error)
	}
	status := response.Status
	if status.Root != root || status.MessageRoot != messageRoot ||
		status.Recipient != command.Recipient ||
		status.Executable != executable ||
		status.Revision != revision {
		return errors.New("wake command differs from the running host's binary or roots")
	}
	return nil
}
