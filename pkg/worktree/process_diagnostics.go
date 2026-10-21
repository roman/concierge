package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
	message.WriteString("; process details are best effort; recorded worker pane is unavailable")
	return message.String()
}

func describeProcessBlockers(ctx context.Context, processes []poolProcess) error {
	// A later snapshot can lose a process or observe a reused PID. Keep every
	// inventory PID in the refusal, even when its diagnostic details are absent.
	output, _ := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,user=,comm=").Output()
	details := parseProcessDetails(string(output))
	failure := &processBlockError{}
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
			Owner:     fields[2],
			Command:   strings.Join(fields[3:], " "),
		}
	}
	return details
}
