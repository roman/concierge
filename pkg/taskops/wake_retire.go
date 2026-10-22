package taskops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

// RetireStalePrimaryWakes acknowledges known wakes for closed tasks or retired
// generations. Unknown keys and generations without retirement evidence stay pending.
func (service *Service) RetireStalePrimaryWakes(ctx context.Context, messageRoot string) error {
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return err
	}
	pending, err := messages.List(service.primaryRecipient)
	if err != nil {
		return err
	}
	for _, message := range pending {
		taskID, generation, ok := primaryWakeGeneration(message.Key, service.root)
		if !ok {
			continue
		}
		_, err := service.store.WithTask(ctx, taskID, func(locked *taskstate.LockedTask) error {
			if !retiredWakeGeneration(locked.Current(), generation) {
				return nil
			}
			if err := locked.CheckCurrent(); err != nil {
				return err
			}
			return messages.Acknowledge(service.primaryRecipient, message.ID)
		})
		if errors.Is(err, taskstate.ErrTaskNotFound) || errors.Is(err, taskstate.ErrInvalidTask) {
			continue
		}
		if err != nil {
			return fmt.Errorf("retire primary wake %s: %w", message.ID, err)
		}
	}
	return ctx.Err()
}

func primaryWakeGeneration(key, root string) (string, string, bool) {
	kind, rest, ok := strings.Cut(key, ":")
	if !ok {
		return "", "", false
	}
	fields := 3
	switch kind {
	case "task-stalled", "task-native-checkpoint":
	case "task-restart":
		fields = 2
	default:
		return "", "", false
	}
	rest, ok = strings.CutPrefix(rest, root+":")
	if !ok {
		return "", "", false
	}
	parts := strings.Split(rest, ":")
	if len(parts) != fields || (fields == 3 && parts[2] == "") {
		return "", "", false
	}
	if _, err := taskmeta.ParseTaskID(parts[0]); err != nil {
		return "", "", false
	}
	if taskmeta.ValidateSpawnGeneration(parts[1]) != nil {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func retiredWakeGeneration(task taskstate.Task, generation string) bool {
	snapshot := task.Snapshot()
	// Closed backlog entries can still have accepted workers awaiting cleanup.
	if worker := snapshot.Worker; worker != nil {
		if worker.Generation == generation || worker.PendingGeneration == generation ||
			worker.PendingLaunch == generation {
			return false
		}
	}
	if intent := snapshot.LaunchIntent; intent != nil && intent.Generation == generation {
		return false
	}
	if task.State() == taskstate.Done || task.HasReceipt(generation, taskstate.Close) ||
		task.HasReceipt(generation, taskstate.Retain) {
		return true
	}
	if completion := snapshot.Completion; completion != nil &&
		completion.Generation == generation && completion.Phase == taskstate.Retired {
		return true
	}
	if _, found := task.LaunchAbandonment(generation); found {
		return true
	}
	if worker := snapshot.Worker; worker != nil {
		for _, restart := range worker.Restarts {
			if restart.From == generation {
				return true
			}
		}
	}
	return false
}
