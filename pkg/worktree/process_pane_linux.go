package worktree

import (
	"os"
	"strconv"
	"strings"
)

func processPaneHint(pid int) string {
	if pid <= 0 {
		return "unavailable"
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "unavailable"
	}
	for _, value := range strings.Split(string(data), "\x00") {
		if pane, found := strings.CutPrefix(value, "HERDR_PANE_ID="); found && pane != "" {
			return pane
		}
	}
	return "unavailable"
}
