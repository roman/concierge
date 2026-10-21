package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	maxProcessDiagnostics   = 16
	maxProcessFieldBytes    = 128
	maxProcessSnapshotBytes = 64 * 1024
)

// blockingProcess describes a PID from the pool's refusal snapshot.
// Details are best effort and never authorize checkout release.
type blockingProcess struct {
	PID                                 int
	ParentPID, Owner, Command, PaneHint string
}

// processBlockError preserves the identity refusal with process diagnostics.
type processBlockError struct {
	Processes []blockingProcess
	Omitted   int
}

func (failure *processBlockError) Unwrap() error { return ErrIdentity }

func (failure *processBlockError) Error() string {
	var message strings.Builder
	fmt.Fprintf(&message, "%s: live processes block checkout return", ErrIdentity)
	for _, process := range failure.Processes {
		fmt.Fprintf(
			&message,
			"; pid=%d owner=%q ppid=%s command=%q pane_hint=%q pane_relation=unknown",
			process.PID,
			process.Owner,
			process.ParentPID,
			process.Command,
			process.PaneHint,
		)
	}
	if failure.Omitted > 0 {
		fmt.Fprintf(&message, "; omitted_processes=%d", failure.Omitted)
	}
	message.WriteString("; process details are best effort; recorded worker pane is unavailable")
	return message.String()
}

func describeProcessBlockers(ctx context.Context, processes []poolProcess) error {
	// A later snapshot can lose a process or observe a reused PID. Preserve
	// the refusal even when diagnostic details are absent or truncated.
	failure := &processBlockError{}
	if len(processes) > maxProcessDiagnostics {
		failure.Omitted = len(processes) - maxProcessDiagnostics
		processes = processes[:maxProcessDiagnostics]
	}
	pids := make([]string, 0, len(processes))
	for _, process := range processes {
		if process.PID > 0 {
			pids = append(pids, strconv.Itoa(process.PID))
		}
	}
	var output processSnapshotBuffer
	if len(pids) > 0 {
		snapshotContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		command := exec.CommandContext(
			snapshotContext,
			"ps",
			"-p",
			strings.Join(pids, ","),
			"-o",
			"pid=,ppid=,user=,comm=",
		)
		command.Stdout = &output
		command.WaitDelay = 100 * time.Millisecond
		_ = command.Run()
	}
	details := parseProcessDetails(output.buffer.String())
	for _, process := range processes {
		detail, found := details[process.PID]
		if !found {
			detail = blockingProcess{
				PID:       process.PID,
				ParentPID: "unavailable",
				Owner:     "unavailable",
				Command:   "unavailable",
			}
		}
		detail.PaneHint = processPaneHint(process.PID)
		failure.Processes = append(failure.Processes, detail)
	}
	return failure
}

// Keep a bounded prefix while draining stdout so diagnostics cannot consume
// unbounded memory or block a child on a full pipe.
type processSnapshotBuffer struct{ buffer bytes.Buffer }

func (output *processSnapshotBuffer) Write(data []byte) (int, error) {
	length := len(data)
	remaining := maxProcessSnapshotBytes - output.buffer.Len()
	if remaining > 0 {
		_, _ = output.buffer.Write(data[:min(length, remaining)])
	}
	return length, nil
}

func processDiagnosticField(value string) string {
	if len(value) > maxProcessFieldBytes {
		return strings.ToValidUTF8(value[:maxProcessFieldBytes], "?") + "..."
	}
	return value
}

func parseProcessDetails(output string) map[int]blockingProcess {
	details := make(map[int]blockingProcess)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidError := strconv.Atoi(fields[0])
		parent, parentError := strconv.Atoi(fields[1])
		if pidError != nil || parentError != nil || pid <= 0 || parent < 0 {
			continue
		}
		details[pid] = blockingProcess{
			PID:       pid,
			ParentPID: strconv.Itoa(parent),
			Owner:     processDiagnosticField(fields[2]),
			Command:   processDiagnosticField(strings.Join(fields[3:], " ")),
		}
	}
	return details
}
