package worktree

import (
	"io"
	"os"
	"strconv"
	"strings"
)

func processPaneHint(pid int) string {
	if pid <= 0 {
		return "unavailable"
	}
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "unavailable"
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxProcessSnapshotBytes))
	if err != nil {
		return "unavailable"
	}
	values := strings.Split(string(data), "\x00")
	// The final segment can be incomplete at the read limit.
	for _, value := range values[:len(values)-1] {
		if pane, found := strings.CutPrefix(value, "HERDR_PANE_ID="); found && pane != "" {
			return processDiagnosticField(pane)
		}
	}
	return "unavailable"
}
