// Package sessionusagecli provides the offline concierge-usage command.
package sessionusagecli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/roman/concierge/pkg/sessionusage"
)

var version = "dev"

type command struct {
	Harness  string `default:"codex" enum:"codex,claude" help:"Saved transcript harness."`
	Sessions string `help:"Session directory; defaults to ~/.codex/sessions or the Claude project directory."`
	Project  string `help:"Exact project working directory filter."`
	From     string `help:"Inclusive start time (RFC 3339)."`
	To       string `help:"Inclusive end time (RFC 3339); defaults to now with a duration."`
	Format   string `default:"toon" enum:"toon,json" help:"Report format. JSON contains all details."`
	Full     bool   `help:"Include per-session details and metric limits in TOON."`
	Duration string `arg:"" optional:"" help:"Trailing window duration, for example 6h; replaces --from."`
}

const help = "concierge-usage --harness codex --project /project --from RFC3339 --to RFC3339 [--format toon|json]; or concierge-usage --project /project [--to RFC3339] 6h"

// Run reads saved files and writes one report. It never accesses live harness state.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v" || args[0] == "-V") {
		fmt.Fprintln(stdout, version)
		return 0
	}
	var command command
	exitCode := -1
	parser, err := kong.New(
		&command,
		kong.Name("concierge-usage"),
		kong.Description(
			"Measure saved Codex or Claude sessions offline.\n\nExamples:\n  concierge-usage --project /project 6h\n  concierge-usage --harness claude --sessions /saved --from 2026-10-20T22:00:00Z --to 2026-10-21T04:00:00Z --format json",
		),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) { exitCode = code }),
	)
	if err != nil {
		return failure(stdout, 2, err)
	}
	if _, err := parser.Parse(args); err != nil {
		return failure(stdout, 2, err)
	}
	if exitCode >= 0 {
		return exitCode
	}
	from, to, err := command.window(time.Now())
	if err != nil {
		return failure(stdout, 2, err)
	}
	directory, err := command.directory()
	if err != nil {
		return failure(stdout, 2, err)
	}
	project := command.Project
	if project != "" {
		project, err = filepath.Abs(project)
		if err != nil {
			return failure(stdout, 2, err)
		}
	}
	report, err := sessionusage.Measure(
		sessionusage.Options{
			Harness:   command.Harness,
			Directory: directory,
			Project:   project,
			From:      from,
			To:        to,
		},
	)
	if err != nil {
		return failure(stdout, 1, err)
	}
	if command.Format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(report)
	} else {
		err = writeTOON(stdout, report, outputOptions{full: command.Full})
	}
	if err != nil {
		return failure(stdout, 1, err)
	}
	return 0
}

func failure(output io.Writer, code int, err error) int {
	fmt.Fprintf(output, "error: %s\nhelp: %s\n", quote(err.Error()), quote(help))
	return code
}

func (c command) window(now time.Time) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	if c.To != "" {
		to, err = time.Parse(time.RFC3339Nano, c.To)
		if err != nil {
			return from, to, err
		}
	}
	if c.Duration != "" {
		if c.From != "" {
			return from, to, errors.New("use a duration or --from, not both")
		}
		duration, err := time.ParseDuration(c.Duration)
		if err != nil || duration <= 0 {
			return from, to, errors.New("duration must be positive, for example 6h")
		}
		if to.IsZero() {
			to = now
		}
		return to.Add(-duration), to, nil
	}
	if c.From == "" || c.To == "" {
		return from, to, errors.New("provide --from and --to, or a trailing duration")
	}
	from, err = time.Parse(time.RFC3339Nano, c.From)
	if err != nil {
		return from, to, err
	}
	if to.Before(from) {
		return from, to, errors.New("--to must be at or after --from")
	}
	return from, to, nil
}

func (c command) directory() (string, error) {
	if c.Sessions != "" {
		return c.Sessions, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if c.Harness == "codex" {
		return filepath.Join(home, ".codex", "sessions"), nil
	}
	if c.Project == "" {
		return "", errors.New(
			"Claude requires --sessions or --project to select a project directory",
		)
	}
	project, err := filepath.Abs(c.Project)
	if err != nil {
		return "", err
	}
	// Claude encodes each non-alphanumeric path character as a hyphen.
	encoded := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			return character
		}
		return '-'
	}, project)
	return filepath.Join(home, ".claude", "projects", encoded), nil
}
