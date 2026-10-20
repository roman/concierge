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

	"github.com/roman/concierge/pkg/heartbeat"
	"github.com/roman/concierge/pkg/stateversion"
)

const heartbeatHelp = "concierge heartbeat show | set [--interval 5m] [--quiet-limit 5] | check [--wake checkpoint|inbox|reminder]"

type heartbeatCommands struct {
	Show struct{} `cmd:"" help:"Show the operator-owned heartbeat interval and quiet limit."`
	Set  struct {
		Interval   *time.Duration `help:"Positive interval between primary heartbeats."`
		QuietLimit *int           `help:"Pause after this many unchanged heartbeats or reminders (default 5). Use 0 to disable pausing."`
	} `cmd:"" help:"Set the operator-approved heartbeat interval or quiet limit."`
	Check struct {
		Wake  string `default:"checkpoint" enum:"checkpoint,inbox,reminder" help:"Use inbox or reminder to defer a heartbeat after another wake."`
		Herdr string `default:"herdr" env:"TINCAN_HERDR" help:"Herdr executable for fresh worker status."`
	} `cmd:"" help:"Claim a due heartbeat at an inbox wait checkpoint."`
}

type heartbeatSettings struct {
	Interval   string `json:"interval"`
	QuietLimit int    `json:"quiet_limit"`
}

func readHeartbeatSettings(path string) (heartbeatSettings, error) {
	settings := heartbeatSettings{Interval: "5m", QuietLimit: 5}
	data, err := readOptionalConfiguration(path)
	if err != nil {
		return settings, err
	}
	if data == nil {
		return settings, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	interval, err := time.ParseDuration(settings.Interval)
	if err != nil || interval <= 0 {
		return settings, errors.New("heartbeat interval must be positive")
	}
	if settings.QuietLimit < 0 {
		return settings, errors.New("heartbeat quiet limit must be nonnegative")
	}
	return settings, nil
}

func (command *cli) runHeartbeat(ctx context.Context, root, selected string, output io.Writer) int {
	path := filepath.Join(root, "config", "heartbeat.json")
	settings, err := readHeartbeatSettings(path)
	if err != nil {
		return fail(output, 1, err, heartbeatHelp)
	}
	if selected == "heartbeat set" {
		if interval := command.Heartbeat.Set.Interval; interval != nil {
			if *interval <= 0 {
				return fail(
					output,
					2,
					errors.New("heartbeat interval must be positive"),
					heartbeatHelp,
				)
			}
			settings.Interval = interval.String()
		}
		if limit := command.Heartbeat.Set.QuietLimit; limit != nil {
			if *limit < 0 {
				return fail(
					output,
					2,
					errors.New("heartbeat quiet limit must be nonnegative"),
					heartbeatHelp,
				)
			}
			settings.QuietLimit = *limit
		}
		data, _ := json.Marshal(settings)
		if err := writeConfiguration(path, append(data, '\n'), func(candidate string) error {
			_, err := readHeartbeatSettings(candidate)
			return err
		}); err != nil {
			return fail(output, 1, err, heartbeatHelp)
		}
	}
	interval, _ := time.ParseDuration(settings.Interval)
	if selected != "heartbeat check" {
		_, err := fmt.Fprintf(
			output,
			"interval: %s\nquiet_limit: %d\n",
			interval,
			settings.QuietLimit,
		)
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

type heartbeatStatus = heartbeat.Observation

type heartbeatLedger struct {
	LastWake       time.Time `json:"last_wake"`
	LastStatus     string    `json:"last_status"`
	ObservedStatus string    `json:"observed_status"`
	QuietBeats     int       `json:"quiet_beats"`
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
	guard, err := stateversion.AcquireCurrent(ctx, root)
	if err != nil {
		return err
	}
	defer guard.Close()
	unlock, err := lockSettings(root, ".heartbeat.lock")
	if err != nil {
		return err
	}
	defer unlock()
	settings, err := readHeartbeatSettings(filepath.Join(root, "config", "heartbeat.json"))
	if err != nil {
		return err
	}
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
	if wake == "checkpoint" && ledger.ObservedStatus != status.Text {
		ledger.QuietBeats = 0
		ledger.ObservedStatus = status.Text
	}
	if !status.Active {
		ledger = heartbeatLedger{}
		view.WriteString("status: quiet\nactive: no\n")
	} else if wake == "checkpoint" && settings.QuietLimit > 0 && ledger.QuietBeats >= settings.QuietLimit {
		view.WriteString("status: quiet\nactive: yes\npaused: yes\n")
	} else if wake != "checkpoint" || ledger.LastWake.IsZero() || now.Before(ledger.LastWake.Add(interval)) {
		if wake != "checkpoint" || ledger.LastWake.IsZero() {
			ledger.LastWake = now.UTC()
		}
		fmt.Fprintf(&view, "status: quiet\nactive: yes\nnext_in: %s\n", ledger.LastWake.Add(interval).Sub(now))
	} else {
		view.WriteString("status: due\nheartbeat: primary\n")
		view.WriteString(status.Text)
		if ledger.LastStatus == status.Text {
			ledger.QuietBeats++
			view.WriteString("changes: no changes since last heartbeat\n")
		}
		if settings.QuietLimit > 0 && ledger.QuietBeats >= settings.QuietLimit {
			view.WriteString("pause: Heartbeats are pausing until something changes.\n")
		}
		view.WriteString("help: Post this status to the operator. End with Waiting on you.\n")
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
	return heartbeat.Collect(ctx, root, messageRoot, recipientName, executable)
}
