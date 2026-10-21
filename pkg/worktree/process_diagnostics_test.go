package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPoolReturnReportsBlockingProcesses(t *testing.T) {
	root := t.TempDir()
	child := exec.Command("sleep", "60")
	child.Dir = root
	child.Env = append(os.Environ(), "HERDR_PANE_ID=diagnostic-fixture-pane")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	for _, test := range []struct {
		name, id string
		want     string
	}{
		{"live process", "lease-1", "live processes block checkout return"},
		{"changed lease", "lease-2", "lease changed or checkout is damaged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executable := filepath.Join(root, "treehouse")
			inventory := fmt.Sprintf(
				`[{"path":%q,"lease_id":%q,"lease_holder":"task-holder","flavor":"git","processes":[{"pid":%d}]}]`,
				filepath.Join(root, "worker"),
				test.id,
				child.Process.Pid,
			)
			script := "#!/bin/sh\n[ \"$1\" = status ] || exit 72\ncat <<'INVENTORY'\n" + inventory + "\nINVENTORY\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			err := (PoolClient{Executable: executable}).Return(
				context.Background(),
				root,
				Lease{Path: filepath.Join(root, "worker"), ID: "lease-1", Holder: "task-holder"},
			)
			if !errors.Is(err, ErrIdentity) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("return refusal: %v", err)
			}
			if test.id != "lease-1" {
				if strings.Contains(err.Error(), "pid=") {
					t.Fatalf("identity failure reports process refusal: %v", err)
				}
				return
			}
			var blocked *processBlockError
			if !errors.As(err, &blocked) || len(blocked.Processes) != 1 {
				t.Fatalf("missing typed process diagnostics: %v", err)
			}
			if blocked.Processes[0].Owner == "unavailable" || blocked.Processes[0].Owner == "" {
				t.Fatalf("owned fixture has no owner: %+v", blocked.Processes[0])
			}
			if runtime.GOOS == "linux" &&
				blocked.Processes[0].PaneHint != "diagnostic-fixture-pane" {
				t.Fatalf("missing inherited pane hint: %+v", blocked.Processes[0])
			}
			for _, want := range []string{"pid=" + strconv.Itoa(child.Process.Pid), "owner=", "ppid=" + strconv.Itoa(os.Getpid()), "command=", "pane_relation=unknown"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestProcessBlockersKeepInventoryPIDsWhenDetailsAreUnavailable(t *testing.T) {
	for _, test := range []struct{ name, script string }{
		{"vanished", "#!/bin/sh\nexit 0\n"},
		{"inaccessible", "#!/bin/sh\nexit 1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "ps"), []byte(test.script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root)
			err := describeProcessBlockers(context.Background(), []poolProcess{{PID: 0}, {PID: -1}})
			if !strings.Contains(err.Error(), "ppid=unavailable") {
				t.Errorf("missing parent appears as a known PID: %v", err)
			}
			var blocked *processBlockError
			if !errors.Is(err, ErrIdentity) || !errors.As(err, &blocked) ||
				len(blocked.Processes) != 2 {
				t.Fatalf("lost refusal or inventory: %v", err)
			}
			for index, pid := range []int{0, -1} {
				process := blocked.Processes[index]
				if process.PID != pid || process.Owner != "unavailable" ||
					process.PaneHint != "unavailable" {
					t.Fatalf("unavailable process: %+v", process)
				}
			}
		})
	}
}

func TestParseProcessDetails(t *testing.T) {
	for _, test := range []struct {
		name, output string
		want         int
	}{
		{"owner and parent", "12 1 roman /path with spaces/shell\n", 1},
		{"malformed", "bad 1 roman shell\n12 bad roman shell\n12 1 roman\n-1 1 roman shell\n", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			details := parseProcessDetails(test.output)
			if len(details) != test.want {
				t.Fatalf("details: %+v", details)
			}
			if test.want == 1 &&
				(details[12].Owner != "roman" || details[12].ParentPID != "1" || details[12].Command != "/path with spaces/shell") {
				t.Fatalf("incorrect process details: %+v", details[12])
			}
		})
	}
}
