package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

const heartbeatHelp = "concierge heartbeat show | set --interval 5m | check [--wake checkpoint|inbox|reminder]"

type heartbeatCommands struct {
	Show struct{} `cmd:"" help:"Show the operator-owned heartbeat interval."`
	Set  struct {
		Interval time.Duration `required:"" help:"Positive interval between primary heartbeats."`
	} `cmd:"" help:"Set the operator-approved heartbeat interval."`
	Check struct {
		Wake  string `default:"checkpoint" enum:"checkpoint,inbox,reminder" help:"Use inbox or reminder to defer a heartbeat after another wake."`
		Herdr string `default:"herdr" env:"TINCAN_HERDR" help:"Herdr executable for fresh worker status."`
	} `cmd:"" help:"Claim a due heartbeat at an inbox wait checkpoint."`
}

type heartbeatSettings struct {
	Interval string `json:"interval"`
}

func readHeartbeatInterval(path string) (time.Duration, error) {
	data, err := readOptionalConfiguration(path)
	if err != nil {
		return 0, err
	}
	if data == nil {
		return 5 * time.Minute, nil
	}
	var settings heartbeatSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return 0, err
	}
	interval, err := time.ParseDuration(settings.Interval)
	if err != nil || interval <= 0 {
		return 0, errors.New("heartbeat interval must be positive")
	}
	return interval, nil
}

func (command *cli) runHeartbeat(ctx context.Context, root, selected string, output io.Writer) int {
	path := filepath.Join(root, "config", "heartbeat.json")
	if selected == "heartbeat set" {
		if command.Heartbeat.Set.Interval <= 0 {
			return fail(output, 2, errors.New("heartbeat interval must be positive"), heartbeatHelp)
		}
		data, _ := json.Marshal(
			heartbeatSettings{Interval: command.Heartbeat.Set.Interval.String()},
		)
		if err := writeConfiguration(path, append(data, '\n'), func(candidate string) error {
			_, err := readHeartbeatInterval(candidate)
			return err
		}); err != nil {
			return fail(output, 1, err, heartbeatHelp)
		}
	}
	interval, err := readHeartbeatInterval(path)
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	if selected != "heartbeat check" {
		_, err := fmt.Fprintf(output, "interval: %s\n", interval)
		if err != nil {
			return 1
		}
		return 0
	}
	wake := command.Heartbeat.Check.Wake
	// Avoid live pane inspection when another wake already supplies the update.
	if wake != "checkpoint" {
		err = claimHeartbeat(
			ctx,
			root,
			interval,
			time.Now(),
			wake,
			heartbeatStatus{Active: true},
			output,
		)
	} else {
		messageRoot, rootErr := rootWithDefault(command.MessageRoot, ".tincan")
		if rootErr != nil {
			return fail(output, 1, rootErr, heartbeatHelp)
		}
		status, statusErr := collectHeartbeatStatus(ctx, root, messageRoot, command.Recipient, command.Heartbeat.Check.Herdr)
		if statusErr != nil {
			return fail(output, 1, statusErr, heartbeatHelp)
		}
		err = claimHeartbeat(ctx, root, interval, time.Now(), wake, status, output)
	}
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	return 0
}

type heartbeatStatus struct {
	Active bool
	Text   string
	Notify bool
}

type heartbeatLedger struct {
	LastWake   time.Time `json:"last_wake"`
	LastStatus string    `json:"last_status"`
}

func claimHeartbeat(
	ctx context.Context,
	root string,
	interval time.Duration,
	now time.Time,
	wake string,
	status heartbeatStatus,
	output io.Writer,
) error {
	guard, err := stateversion.AcquireVersionTwo(ctx, root)
	if err != nil {
		return err
	}
	defer guard.Close()
	unlock, err := lockSettings(root, ".heartbeat.lock")
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(root, "heartbeat.json")
	data, err := readOptionalConfiguration(path)
	if err != nil {
		return err
	}
	var ledger heartbeatLedger
	if data != nil {
		if err := json.Unmarshal(data, &ledger); err != nil {
			return err
		}
	}
	var view strings.Builder
	if !status.Active {
		ledger = heartbeatLedger{}
		view.WriteString("status: quiet\nactive: no\n")
	} else if wake != "checkpoint" || ledger.LastWake.IsZero() || now.Before(ledger.LastWake.Add(interval)) {
		if wake != "checkpoint" || ledger.LastWake.IsZero() {
			ledger.LastWake = now.UTC()
		}
		fmt.Fprintf(&view, "status: quiet\nactive: yes\nnext_in: %s\n", ledger.LastWake.Add(interval).Sub(now))
	} else {
		view.WriteString("status: due\nheartbeat: primary\n")
		view.WriteString(status.Text)
		if ledger.LastStatus == status.Text {
			view.WriteString("changes: no changes since last heartbeat\n")
		}
		view.WriteString("help: Post this status to the operator even when nothing changed. End with Waiting on you.\n")
		if status.Notify {
			view.WriteString("notification: Send a desktop notification for the waiting decision or idle finished worker (Claude: PushNotification).\n")
		}
		ledger.LastWake = now.UTC()
		ledger.LastStatus = status.Text
	}
	updated, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	if string(updated) != string(data) {
		if err := writeConfiguration(path, updated, func(candidate string) error {
			data, err := readOptionalConfiguration(candidate)
			if err != nil {
				return err
			}
			var check heartbeatLedger
			return json.Unmarshal(data, &check)
		}); err != nil {
			return err
		}
	}
	_, err = io.WriteString(output, view.String())
	return err
}

func collectHeartbeatStatus(
	ctx context.Context,
	root, messageRoot, recipientName, executable string,
) (heartbeatStatus, error) {
	store, err := taskstate.NewStore(root)
	if err != nil {
		return heartbeatStatus{}, err
	}
	tasks, err := store.List(ctx)
	if err != nil {
		return heartbeatStatus{}, err
	}
	ledger, err := readOperatorLedger(filepath.Join(root, "operator-actions.json"))
	if err != nil {
		return heartbeatStatus{}, err
	}
	recipient, err := inbox.ParseRecipient(recipientName)
	if err != nil {
		return heartbeatStatus{}, err
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return heartbeatStatus{}, err
	}
	pending, err := messages.List(recipient)
	if err != nil {
		return heartbeatStatus{}, err
	}
	replies := 0
	var replyRows strings.Builder
	workerReplies := map[string]bool{}
	for _, message := range pending {
		if strings.HasPrefix(message.Key, "task-reply:"+root+":") {
			replies++
			fmt.Fprintf(&replyRows, "pending_reply: %s\n", message.ID.String())
			identity := strings.TrimPrefix(message.Key, "task-reply:"+root+":")
			if separator := strings.LastIndex(identity, ":"); separator >= 0 {
				workerReplies[identity[:separator]] = true
			}
		}
	}
	var workers strings.Builder
	bound, completions := 0, 0
	notify := len(ledger.Actions) > 0
	for _, task := range tasks {
		snapshot := task.Snapshot()
		if snapshot.Completion != nil {
			completions++
			fmt.Fprintf(
				&workers,
				"cleanup: %s, %s, %s\n",
				quote(task.ID()),
				snapshot.Completion.Phase,
				quote(snapshot.Completion.Generation),
			)
		}
		worker := snapshot.Worker
		if worker == nil {
			continue
		}
		bound++
		client, err := herdr.NewClient(executable).ForSession(worker.Session)
		if err != nil {
			return heartbeatStatus{}, err
		}
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		agent, err := client.Get(statusCtx, worker.Pane)
		cancel()
		state := agent.Status
		if err != nil {
			state = herdr.Unknown
		}
		fmt.Fprintf(
			&workers,
			"worker: %s, %s, %s\n",
			quote(task.ID()),
			state,
			quote(worker.Generation),
		)
		finished := snapshot.Completion != nil || workerReplies[task.ID()+":"+worker.Generation]
		if state == herdr.Done || (state == herdr.Idle && finished) {
			notify = true
		}
	}
	var view strings.Builder
	fmt.Fprintf(
		&view,
		"workers: %d bound\npending_replies: %d\nawaiting_cleanup: %d\n",
		bound,
		replies,
		completions,
	)
	view.WriteString(workers.String())
	view.WriteString(replyRows.String())
	writeOperatorActions(&view, ledger.Actions)
	return heartbeatStatus{
		Active: bound > 0 || len(ledger.Actions) > 0,
		Text:   view.String(),
		Notify: notify,
	}, nil
}
