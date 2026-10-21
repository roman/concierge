package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestProcessBlockerDiagnosticsAreBounded(t *testing.T) {
	root := t.TempDir()
	script := "#!/bin/sh\nprintf '12 1 owner '; printf '%08000d' 0; printf '\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	processes := make([]poolProcess, 100)
	for index := range processes {
		processes[index].PID = 12
	}
	err := describeProcessBlockers(context.Background(), processes)
	if !errors.Is(err, ErrIdentity) {
		t.Fatalf("lost ownership refusal: %v", err)
	}
	if len(err.Error()) > 16000 || !strings.Contains(err.Error(), "omitted_processes=84") {
		t.Fatalf("unbounded diagnostics: bytes=%d, omitted count present=%t",
			len(err.Error()), strings.Contains(err.Error(), "omitted_processes=84"))
	}
}

func TestProcessDiagnosticsDoNotWaitForStuckSnapshot(t *testing.T) {
	for _, test := range []struct {
		name, script string
		limit        time.Duration
	}{
		{"inherited stdout", "#!/bin/sh\n(sleep 2) &\nexit 0\n", time.Second},
		{"stuck ps", "#!/bin/sh\nexec sleep 6\n", 4 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProcessSnapshotDeadline(t, test.script, test.limit)
		})
	}
}

func testProcessSnapshotDeadline(t *testing.T, script string, limit time.Duration) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	err := describeProcessBlockers(context.Background(), []poolProcess{{PID: 0}, {PID: 2147483647}})
	if elapsed := time.Since(start); elapsed > limit {
		t.Fatalf("snapshot blocked refusal for %v", elapsed)
	}
	if !errors.Is(err, ErrIdentity) || !strings.Contains(err.Error(), "pid=2147483647") {
		t.Fatalf("lost blocker after snapshot failure: %v", err)
	}
}

func TestProcessSnapshotQueriesOnlyListedPositivePIDs(t *testing.T) {
	root := t.TempDir()
	script := "#!/bin/sh\n[ \"$*\" = '-p 12,34 -o pid=,ppid=,user=,comm=' ] || exit 1\nprintf '12 1 fixture-owner fixture-command\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	err := describeProcessBlockers(
		context.Background(),
		[]poolProcess{{PID: -1}, {PID: 12}, {PID: 0}, {PID: 34}},
	)
	if !strings.Contains(err.Error(), `owner="fixture-owner"`) {
		t.Fatalf("snapshot query did not select only the listed positive PIDs: %v", err)
	}
}

func TestProcessSnapshotRetainsBoundedPrefix(t *testing.T) {
	var output processSnapshotBuffer
	for range 3 {
		data := []byte(strings.Repeat("x", 40000))
		written, err := output.Write(data)
		if err != nil || written != len(data) {
			t.Fatalf("snapshot writer stopped draining: %d, %v", written, err)
		}
	}
	if output.buffer.Len() != 64*1024 {
		t.Fatalf("snapshot bytes: %d", output.buffer.Len())
	}
}

func TestProcessPaneHintLimits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pane hints require procfs")
	}
	for _, test := range []struct {
		name        string
		environment []string
		want        string
	}{
		{"long hint", []string{"HERDR_PANE_ID=" + strings.Repeat("p", 1000)}, strings.Repeat("p", 128) + "..."},
		{"hint beyond limit", []string{"PADDING=" + strings.Repeat("x", 65536), "HERDR_PANE_ID=hidden"}, "unavailable"},
		{"other values", []string{"SECRET=private-fixture"}, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			child := exec.Command("sh", "-c", "printf ready; read value")
			child.Env = test.environment
			input, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = input.Close(); _ = child.Wait() })
			if _, err := io.ReadFull(output, make([]byte, len("ready"))); err != nil {
				t.Fatal(err)
			}
			if got := processPaneHint(child.Process.Pid); got != test.want {
				t.Fatalf("pane hint: got %q, want %q", got, test.want)
			}
		})
	}
}
