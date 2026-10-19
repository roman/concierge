package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var ErrNativeStartup = errors.New("native startup failed and returned to the shell")

func nativeErrorLine(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Error:") &&
			!strings.HasPrefix(line, "Error adding directories:") &&
			!strings.HasPrefix(line, "error:") {
			continue
		}
		line = strings.Map(func(character rune) rune {
			if unicode.IsControl(character) {
				return -1
			}
			return character
		}, line)
		if runes := []rune(line); len(runes) > 512 {
			line = string(runes[:512])
		}
		return line
	}
	return ""
}

// A fresh fatal diagnostic and two shell-only samples can end Herdr's startup
// wait. An unreadable endpoint retains the original startup request and timeout.
func (client Client) runNativeStart(ctx context.Context, pane string, arguments []string) error {
	if client.machine == "" || client.MachineRunner == nil {
		_, err := client.shared().Run(ctx, arguments...)
		return err
	}
	baseline, err := client.VisibleScreen(ctx, pane)
	if err != nil {
		_, err = client.shared().Run(ctx, arguments...)
		return err
	}
	target, err := ParseEndpoint(client.machine, client.session, pane)
	if err != nil {
		return err
	}
	startup, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.shared().Run(startup, arguments...); done <- err }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	previous := ""
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			inspection, stop := context.WithTimeout(startup, time.Second)
			class, _ := client.processSample(inspection, target)
			screen, screenErr := client.VisibleScreen(inspection, pane)
			stop()
			line := nativeErrorLine(screen)
			if class != processShell || screenErr != nil || line == "" ||
				strings.Contains(baseline, line) {
				previous = ""
				continue
			}
			if line != previous {
				previous = line
				continue
			}
			cancel()
			<-done
			return fmt.Errorf(
				"%w: %s; inspect the recorded pane, then close its launch resources before task abandon",
				ErrNativeStartup,
				line,
			)
		}
	}
}
