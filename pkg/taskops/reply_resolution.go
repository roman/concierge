package taskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func (service *Service) replyResolutionBinding(
	current taskstate.Task,
	messages *inbox.Store,
	id inbox.MessageID,
	reply taskprotocol.Reply,
	mode taskstate.CompletionMode,
	closedOn string,
) (string, string, error) {
	generation := reply.SpawnGeneration
	existing, dispositionErr := messages.ReadDisposition(service.primaryRecipient, id)
	suffix := ":" + string(mode)
	if mode == taskstate.Close {
		suffix += ":" + closedOn
	}
	prefix := "finish:" + reply.Task + ":"
	if dispositionErr == nil {
		if !strings.HasPrefix(existing.Action, prefix) ||
			!strings.HasSuffix(existing.Action, suffix) {
			return "", "", inbox.ErrDispositionConflict
		}
		generation = strings.TrimSuffix(strings.TrimPrefix(existing.Action, prefix), suffix)
		if taskmeta.ValidateSpawnGeneration(generation) != nil {
			return "", "", inbox.ErrDispositionConflict
		}
		state, err := resultState(mode)
		if err != nil {
			return "", "", err
		}
		if existing.State == inbox.DispositionComplete {
			if existing.Outcome != string(state) ||
				!resultAlreadyApplied(current, generation, mode) {
				return "", "", inbox.ErrDispositionConflict
			}
		}
	} else if !errors.Is(dispositionErr, os.ErrNotExist) {
		return "", "", dispositionErr
	} else if worker, err := assignmentWorker(current); err == nil && preservedReply(worker, id.String()) {
		generation = worker.Generation
	}
	if (mode == taskstate.Retain && closedOn != "") ||
		(mode == taskstate.Close && (closedOn == "" || (resultAlreadyApplied(current, generation, mode) && current.Definition().Closed != closedOn))) {
		return "", "", taskstate.ErrInvalidTransition
	}
	return generation, fmt.Sprintf("finish:%s:%s%s", reply.Task, generation, suffix), nil
}

func (service *Service) prepareReplyAcceptance(
	ctx context.Context,
	messages *inbox.Store,
	id inbox.MessageID,
	reply taskprotocol.Reply,
	mode taskstate.CompletionMode,
	closedOn string,
) error {
	var generation string
	_, err := service.store.WithTask(ctx, reply.Task, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		var err error
		generation, _, err = service.replyResolutionBinding(
			current,
			messages,
			id,
			reply,
			mode,
			closedOn,
		)
		if err != nil {
			return err
		}
		if current.HasReceipt(generation, mode) {
			return nil
		}
		return current.CheckResultTransition(generation, mode, closedOn)
	})
	if err != nil {
		return err
	}
	return service.prepareRemoteAcceptance(ctx, reply.Task, generation, mode, closedOn)
}
