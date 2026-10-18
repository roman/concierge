package integration

import (
	"os"
	"testing"
)

// This test uses real SSH and native agent permissions. Ordinary test runs never
// contact a remote machine. The launcher requires committed source and writes a report.
func TestSavedMachineAcceptance(t *testing.T) {
	if os.Getenv("CONCIERGE_ACCEPTANCE_REMOTE") != "1" {
		t.Skip("run scripts/acceptance-remote.sh from a native primary session")
	}
	run := newSavedMachineAcceptance(t)
	run.prepare()
	scout := run.launch("scout")
	run.restartPrimary(scout)
	run.dropAndRecoverTunnel(scout)
	run.acceptAndClean(scout)
	ship := run.launch("ship")
	run.acceptAndClean(ship)
	run.verifyComplete()
}
