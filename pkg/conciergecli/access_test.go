package conciergecli

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/roman/concierge/pkg/primary"
)

func TestAccessFailureNamesSingleNativeRetry(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"process identity", fmt.Errorf("claim: %w", primary.ErrProcessOwnership)},
		{"Unix socket", &net.OpError{Op: "dial", Net: "unix", Err: syscall.EPERM}},
		{"read-only state", &os.PathError{Op: "openat", Path: ".state.lock", Err: syscall.EROFS}},
		{"denied state", &os.PathError{Op: "openat", Path: ".state.lock", Err: syscall.EACCES}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if code := fail(&output, 7, test.err, "preserve the pending event"); code != 7 {
				t.Fatalf("exit code = %d", code)
			}
			for _, text := range []string{
				"preserve the pending event",
				"retry the exact command once",
				"sandbox_permissions=require_escalated",
				"native approval",
				"If denied, keep the work pending",
			} {
				if !strings.Contains(output.String(), text) {
					t.Fatalf("missing %q: %s", text, &output)
				}
			}
		})
	}
}

func TestOrdinaryFailureKeepsExistingHelp(t *testing.T) {
	for _, err := range []error{
		errors.New("generation mismatch"),
		&net.OpError{Op: "dial", Net: "unix", Err: syscall.ENOENT},
		&net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED},
		&os.PathError{Op: "open", Path: "state", Err: syscall.ENOENT},
	} {
		var output bytes.Buffer
		fail(&output, 1, err, "inspect state")
		if strings.Contains(output.String(), "require_escalated") {
			t.Fatalf("ordinary failure suggests escalation: %s", &output)
		}
	}
}
