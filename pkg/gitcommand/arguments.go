// Package gitcommand supplies process-local settings for Concierge's Git commands.
package gitcommand

// Arguments prevents Git from starting a checkout's fsmonitor during inspection.
// The override leaves user and repository configuration unchanged.
func Arguments(directory string, arguments ...string) []string {
	return append([]string{"-C", directory, "-c", "core.fsmonitor=false"}, arguments...)
}
