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

const heartbeatHelp = "concierge heartbeat show | set [--interval 5m] [--quiet-limit 5] | enable | pause --reason REASON | resume | start < SESSION_START_JSON | register codex --socket PATH --thread UUID | claim EVENT --generation GEN | ack EVENT --generation GEN --token TOKEN --output REFERENCE | activity < HOOK_JSON | wait [--timeout 7h59m] < STOP_JSON; check is host-owned after activation"

type heartbeatCommands struct {
	Start    struct{} `cmd:"" help:"Register and activate Codex delivery from the adopted native SessionStart hook on stdin; preserve operator pauses."`
	Activity struct{} `cmd:"" help:"Record a synchronous native Claude lifecycle hook from stdin."`
	Wait     struct {
		Timeout time.Duration `default:"7h59m" help:"Registered Stop consumer lifetime; at most 7h59m."`
	} `cmd:"" help:"Wait for host events inside a verified native asyncRewake Stop hook."`
	Enable struct{} `cmd:"" help:"Explicitly activate the durable host scheduler; preserve an earlier operator pause."`
	Pause  struct {
		Reason string `required:"" help:"Operator reason for pausing periodic output and delivery."`
	} `cmd:"" help:"Persist an operator pause independently of quiet suppression."`
	Resume   struct{} `cmd:"" help:"Explicitly resume periodic events after an operator pause."`
	Register struct {
		Codex struct {
			Socket string `required:"" help:"Explicit owning app-server Unix socket."`
			Thread string `required:"" help:"Exact inherited native thread UUID."`
		} `cmd:"" help:"Register the owning Codex server and thread."`
	} `cmd:"" help:"Bind a native delivery destination to the current primary."`
	Claim struct {
		heartbeatEventSelection
		Herdr string `default:"herdr" env:"TINCAN_HERDR" help:"Herdr executable for fresh worker status."`
	} `cmd:"" help:"Claim a published current event and fetch fresh supervisor state."`
	Ack struct {
		heartbeatEventSelection
		Token  string `required:"" help:"Token returned by heartbeat claim."`
		Output string `required:"" help:"Reference to the operator update already posted."`
	} `cmd:"" help:"Record output after posting; retain work on a stale or expired claim."`
	Show struct{} `cmd:"" help:"Show the operator-owned heartbeat interval and quiet limit."`
	Set  struct {
		Interval   *time.Duration `help:"Positive interval between primary heartbeats."`
		QuietLimit *int           `help:"Suppress unchanged periodic output after this many reported updates (default 5). Use 0 to disable quiet suppression."`
	} `cmd:"" help:"Set the operator-approved heartbeat interval or quiet limit."`
	Check struct {
		Wake  string `default:"checkpoint" enum:"checkpoint,inbox,reminder" help:"Use inbox or reminder to defer a heartbeat after another wake."`
		Herdr string `default:"herdr" env:"TINCAN_HERDR" help:"Herdr executable for fresh worker status."`
	} `cmd:"" help:"Claim a due heartbeat at an inbox wait checkpoint."`
}

type heartbeatEventSelection struct {
	EventID    string `arg:"" help:"Stable supervisor event ID."`
	Generation string `required:"" help:"Primary binding generation from the native notification."`
}

type heartbeatSettings = heartbeat.Settings

func readHeartbeatSettings(path string) (heartbeatSettings, error) {
	return heartbeat.ReadSettings(path)
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
		return writeHeartbeatState(ctx, root, output)
	}
	wake := command.Heartbeat.Check.Wake
	if owned, err := hostOwnsHeartbeat(ctx, root); err != nil {
		return fail(output, 1, err, heartbeatHelp)
	} else if owned {
		fmt.Fprintln(output, "status: quiet\nscheduler: host_owned")
		return writeHeartbeatState(ctx, root, output)
	}
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
