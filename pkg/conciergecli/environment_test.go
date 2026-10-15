package conciergecli

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Kong reads these defaults even when a test supplies an explicit state root.
	// Clear them before any test or native-parent subprocess can use caller state.
	for _, name := range []string{"CONCIERGE_HOME", "TINCAN_HOME", "TINCAN_HERDR", "HERDR_SESSION"} {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}
