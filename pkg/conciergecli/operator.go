package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
)

const operatorHelp = "concierge operator ask ID TEXT | resolve ID | list | remind [--interval 15m]"

type operatorCommands struct {
	Ask struct {
		ID   string `arg:"" help:"Stable action ID; reuse only for the same open request."`
		Text string `arg:"" help:"Approval, command, or decision the operator must supply."`
	} `cmd:"" help:"Record an open operator request before asking the operator."`
	Resolve struct {
		ID string `arg:"" help:"Action ID answered by the operator."`
	} `cmd:"" help:"Remove an answered request; already resolved requests succeed."`
	List   struct{} `cmd:"" help:"List all open operator requests."`
	Remind struct {
		Interval time.Duration `default:"15m" help:"Minimum time between reminders for each item."`
	} `cmd:"" help:"Claim due reminders for open requests and unfinished worker retirement."`
}

type operatorLedger struct {
	Actions        map[string]string    `json:"actions"`
	Reminded       map[string]time.Time `json:"reminded"`
	ActionRevision uint64               `json:"action_revision"`
	ReminderItems  string               `json:"reminder_items"`
	ReminderCount  int                  `json:"reminder_count"`
}

func readOperatorLedger(path string) (operatorLedger, error) {
	ledger := operatorLedger{Actions: map[string]string{}, Reminded: map[string]time.Time{}}
	data, err := readOptionalConfiguration(path)
	if err != nil || data == nil {
		return ledger, err
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		return ledger, err
	}
	if ledger.Actions == nil || ledger.Reminded == nil {
		return ledger, errors.New("invalid operator action ledger")
	}
	for id, text := range ledger.Actions {
		if _, err := taskmeta.ParseTaskID(id); err != nil || strings.TrimSpace(text) == "" {
			return ledger, errors.New("invalid operator action")
		}
	}
	return ledger, nil
}

func withOperatorLedger(
	ctx context.Context,
	root string,
	update func(*operatorLedger) error,
) error {
	guard, err := stateversion.AcquireCurrent(ctx, root)
	if err != nil {
		return err
	}
	defer guard.Close()
	unlock, err := lockSettings(root, ".operator-actions.lock")
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(root, "operator-actions.json")
	ledger, err := readOperatorLedger(path)
	if err != nil {
		return err
	}
	before, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err := update(&ledger); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if bytes.Equal(before, data) {
		return nil
	}
	if err := writeConfiguration(path, append(data, '\n'), func(candidate string) error {
		_, err := readOperatorLedger(candidate)
		return err
	}); err != nil {
		return err
	}
	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (command operatorCommands) run(
	ctx context.Context,
	root, selected string,
	output io.Writer,
) int {
	id := command.Ask.ID
	if selected == "operator resolve <id>" {
		id = command.Resolve.ID
	}
	if selected != "operator list" {
		if _, err := taskmeta.ParseTaskID(id); err != nil {
			return fail(output, 2, err, operatorHelp)
		}
	}
	if selected == "operator ask <id> <text>" && strings.TrimSpace(command.Ask.Text) == "" {
		return fail(output, 2, errors.New("action text must not be empty"), operatorHelp)
	}
	if selected == "operator list" {
		if err := writeOpenOperatorActions(ctx, root, output); err != nil {
			return fail(output, 1, err, operatorHelp)
		}
		return 0
	}
	err := withOperatorLedger(ctx, root, func(ledger *operatorLedger) error {
		if selected == "operator resolve <id>" {
			if _, found := ledger.Actions[id]; found {
				ledger.ActionRevision++
				ledger.ReminderCount = 0
				ledger.ReminderItems = ""
			}
			delete(ledger.Actions, id)
			delete(ledger.Reminded, "action:"+id)
			return nil
		}
		if existing, found := ledger.Actions[id]; found && existing != command.Ask.Text {
			return fmt.Errorf(
				"action %s already has different text; resolve it before reusing the ID",
				id,
			)
		}
		ledger.Actions[id] = command.Ask.Text
		ledger.ActionRevision++
		ledger.ReminderCount = 0
		ledger.ReminderItems = ""
		return nil
	})
	if err != nil {
		return fail(output, 1, err, operatorHelp)
	}
	if err := writeOpenOperatorActions(ctx, root, output); err != nil {
		return fail(output, 1, err, operatorHelp)
	}
	return 0
}

func writeOpenOperatorActions(ctx context.Context, root string, output io.Writer) error {
	guard, err := stateversion.AcquireCurrent(ctx, root)
	if err != nil {
		return err
	}
	defer guard.Close()
	ledger, err := readOperatorLedger(filepath.Join(root, "operator-actions.json"))
	if err != nil {
		return err
	}
	return writeOperatorActions(output, ledger.Actions)
}

func writeOperatorActions(output io.Writer, actions map[string]string) error {
	var view strings.Builder
	fmt.Fprintf(&view, "operator_actions: %d open\n", len(actions))
	writeOperatorRows(&view, "operator_actions", actions)
	_, err := io.WriteString(output, view.String())
	return err
}

func writeOperatorRows(view *strings.Builder, section string, items map[string]string) {
	if len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Fprintf(view, "%s[%d]{id,text}:\n", section, len(ids))
	for _, id := range ids {
		fmt.Fprintf(view, "  %s,%s\n", quote(id), quote(items[id]))
	}
}

func runOperatorReminder(
	ctx context.Context,
	root string,
	interval time.Duration,
	now time.Time,
	output io.Writer,
) int {
	if interval <= 0 {
		return fail(output, 2, errors.New("reminder interval must be positive"), operatorHelp)
	}
	settings, err := readHeartbeatSettings(filepath.Join(root, "config", "heartbeat.json"))
	if err != nil {
		return fail(output, 1, err, operatorHelp)
	}
	store, err := taskstate.NewStore(root)
	if err != nil {
		return fail(output, 2, err, operatorHelp)
	}
	tasks, err := store.List(ctx)
	if err != nil {
		return fail(output, 1, err, operatorHelp)
	}
	var view strings.Builder
	err = withOperatorLedger(ctx, root, func(ledger *operatorLedger) error {
		items := map[string]string{}
		for id, text := range ledger.Actions {
			items["action:"+id] = text
		}
		for _, task := range tasks {
			completion := task.Snapshot().Completion
			if completion == nil {
				continue
			}
			key := fmt.Sprintf("worker:%s:%s", task.ID(), completion.Generation)
			next := "Verify the worker is idle. Land if required. Stop and clean up the worker."
			if completion.Phase == taskstate.Retired {
				next = "Finish the remaining completion operation."
			}
			items[key] = fmt.Sprintf(
				"Task %s generation %s has completion phase %s. %s",
				task.ID(),
				completion.Generation,
				completion.Phase,
				next,
			)
		}
		encodedItems, err := json.Marshal(items)
		if err != nil {
			return err
		}
		if ledger.ReminderItems != string(encodedItems) {
			ledger.ReminderCount = 0
			ledger.ReminderItems = string(encodedItems)
		}
		paused := settings.QuietLimit > 0 && ledger.ReminderCount >= settings.QuietLimit
		due := map[string]string{}
		for key, text := range items {
			last, found := ledger.Reminded[key]
			if !paused && (!found || !now.Before(last.Add(interval))) {
				due[key] = text
				ledger.Reminded[key] = now.UTC()
			}
		}
		for key := range ledger.Reminded {
			if _, found := items[key]; !found {
				delete(ledger.Reminded, key)
			}
		}
		status := "quiet"
		if len(due) > 0 {
			ledger.ReminderCount++
			status = "due"
		}
		fmt.Fprintf(&view, "status: %s\nopen_items: %d\n", status, len(items))
		if len(due) > 0 {
			if settings.QuietLimit > 0 && ledger.ReminderCount >= settings.QuietLimit {
				view.WriteString(
					"pause: Reminders are pausing until the item set changes or the operator replies.\n",
				)
			}
			fmt.Fprintf(&view, "reminders: %d due\n", len(due))
			writeOperatorRows(&view, "reminders", due)
			fmt.Fprintf(
				&view,
				"help: %s\n",
				quote(
					"Repeat these open items to the operator. End the reply with a short Waiting on you list. If the operator has not replied, send a desktop notification through the harness notification tool when available (Claude: PushNotification).",
				),
			)
		}
		return nil
	})
	if err != nil {
		return fail(output, 1, err, operatorHelp)
	}
	if _, err := io.WriteString(output, view.String()); err != nil {
		return 1
	}
	return 0
}
