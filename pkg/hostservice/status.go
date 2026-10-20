package hostservice

import (
	"errors"
	"os"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/supervise"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/tincan/pkg/inbox"
)

const HostStatus Operation = "host_status"

type StatusRequest struct{}

type StatusResult struct {
	Recovery        RecoveryPolicy   `json:"recovery,omitempty"`
	Root            string           `json:"root"`
	MessageRoot     string           `json:"message_root"`
	Recipient       string           `json:"recipient"`
	Session         string           `json:"session"`
	Executable      string           `json:"executable"`
	Revision        string           `json:"revision,omitempty"`
	HerdrExecutable string           `json:"herdr_executable,omitempty"`
	Supervision     supervise.Config `json:"supervision,omitempty"`
}

func authorizeStatusCaller(caller primary.Caller) error {
	if caller.UID != uint32(os.Geteuid()) {
		return errors.New("host status requires the host user")
	}
	return nil
}

func (server *Server) status() *StatusResult {
	return &StatusResult{
		Root: server.config.Root, MessageRoot: server.config.MessageRoot,
		Recipient: server.config.Recipient.String(), Session: server.config.Session,
		Executable:      server.config.WorkerCommands.Concierge,
		Revision:        server.config.Revision,
		HerdrExecutable: server.config.HerdrExecutable,
		Supervision:     server.config.Supervision,
		Recovery:        server.config.Recovery,
	}
}

func (server *Server) statusResponse(caller primary.Caller, request Request) Response {
	if err := authorizeStatusCaller(caller); err != nil {
		return Response{Outcome: Unauthorized, Error: err.Error()}
	}
	if err := server.checkExpectations(request.Expectations); err != nil {
		return Response{Outcome: Invalid, Error: err.Error()}
	}
	return Response{Outcome: Completed, Status: server.status()}
}

func validateStatusResponse(response Response) error {
	if response.Status == nil || response.Task != nil || response.Transition != nil ||
		response.Supervision != nil ||
		response.Control != nil ||
		response.Assignment != nil ||
		response.Stopped != nil ||
		response.Resolution != nil ||
		response.Error != "" {
		return errors.New("host returned an incomplete status result")
	}
	status := response.Status
	if err := status.Recovery.validate(); err != nil {
		return err
	}
	for _, path := range []string{status.Root, status.MessageRoot, status.Executable} {
		if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
			return err
		}
	}
	if _, err := inbox.ParseRecipient(status.Recipient); err != nil {
		return err
	}
	if status.Session != "" {
		if _, err := herdr.ParseTarget(status.Session, "pane"); err != nil {
			return err
		}
	}
	return nil
}
