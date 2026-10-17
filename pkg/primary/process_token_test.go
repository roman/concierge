package primary

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestProcessTokenUsesBootNamespaceAndStartTime(t *testing.T) {
	token := ProcessToken{PID: 123, StartTicks: "100", BootID: "boot", PIDNamespace: "namespace"}
	for _, test := range []struct {
		name      string
		boot      string
		namespace string
		process   Process
		err       error
		want      ProcessStatus
		wantError bool
	}{
		{name: "same process", boot: "boot", namespace: "namespace", process: Process{PID: 123, StartTicks: "100"}, want: ProcessRunning},
		{name: "reused pid", boot: "boot", namespace: "namespace", process: Process{PID: 123, StartTicks: "101"}, want: ProcessExited},
		{name: "prior boot", boot: "new-boot", namespace: "namespace", want: ProcessExited},
		{name: "missing process", boot: "boot", namespace: "namespace", err: os.ErrNotExist, want: ProcessExited},
		{name: "reaped after opening stat", boot: "boot", namespace: "namespace", err: syscall.ESRCH, want: ProcessExited},
		{name: "other namespace", boot: "boot", namespace: "other", wantError: true},
		{name: "unreadable process", boot: "boot", namespace: "namespace", err: os.ErrPermission, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(int) (Process, error) { return test.process, test.err }
			got, err := probeProcessToken(token, test.boot, test.namespace, lookup)
			if test.wantError {
				if !errors.Is(err, ErrProcessOwnership) || got != "" {
					t.Fatalf("uncertain ownership: status=%q error=%v", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("status=%q error=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestCaptureProcessTokenIdentifiesCurrentProcess(t *testing.T) {
	token, err := CaptureProcessToken(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	status, err := ProbeProcessToken(token)
	if err != nil || status != ProcessRunning {
		t.Fatalf("current process: status=%q error=%v", status, err)
	}
	token.StartTicks += "-different"
	status, err = ProbeProcessToken(token)
	if err != nil || status != ProcessExited {
		t.Fatalf("different incarnation: status=%q error=%v", status, err)
	}
}
