package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/presentation"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskstate"
)

func (run *savedMachineAcceptance) cleanup() {
	defer run.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	safe := true
	for _, id := range run.tasks {
		if err := run.retireAcceptanceTask(ctx, id); err != nil {
			run.t.Errorf("cleanup task %s: %v", id, err)
			safe = false
		}
		store, err := remoteworker.NewTunnelStore(run.root)
		if err != nil {
			run.t.Errorf("cleanup tunnel store: %v", err)
			safe = false
			continue
		}
		err = store.WithTask(ctx, id, func(lease *remoteworker.TunnelLease) error {
			record, err := lease.Read()
			if err != nil {
				return err
			}
			if record != nil && record.Process != nil {
				return (remoteworker.SSHProcesses{}).Stop(ctx, *record.Process)
			}
			return nil
		})
		if err != nil {
			run.t.Errorf("cleanup owned tunnel %s: %v", id, err)
			safe = false
		}
	}
	if run.host != nil {
		output, err := run.call(ctx, nil, "host", "stop", "--timeout", "30s")
		if err != nil {
			run.t.Errorf("stop temporary host: %v %s", err, output)
			safe = false
		}
		if err == nil {
			select {
			case <-run.hostDone:
			case <-ctx.Done():
				run.t.Errorf("original temporary host did not exit: %v", ctx.Err())
				safe = false
			}
		}
	}
	if run.remoteDirectory != "" && safe {
		operation := removeSavedMachineAcceptanceDirectory
		if run.fixedBase != "" {
			operation = releaseFixedAcceptanceBase
		}
		output, err := run.ssh(
			ctx,
			"sh",
			"-c",
			operation,
			"concierge-acceptance",
			run.remoteDirectory,
			run.owner,
		)
		if err != nil {
			run.t.Errorf("owned remote cleanup unconfirmed: %v %s", err, output)
			safe = false
		}
		if safe {
			if run.fixedBase != "" {
				run.note(
					"Fixed acceptance base released; prepared clients and trusted pool slots retained at %s",
					run.remoteDirectory,
				)
			} else {
				run.note("Owned remote temporary directory deleted after checking for live processes")
			}
		}
	}
	if safe && !run.t.Failed() {
		if err := os.RemoveAll(run.directory); err != nil {
			run.t.Errorf("local fixture cleanup: %v", err)
			safe = false
		}
	} else {
		run.note("Inspection required: local state %s; remote directory %s", run.directory, run.remoteDirectory)
	}
	status := "passed"
	if run.t.Failed() || !safe {
		status = "failed"
	}
	report := fmt.Sprintf(
		"# Saved machine remote acceptance\n\nStatus: %s\nSource commit: `%s`\n\n%s",
		status,
		run.commit,
		run.transcript.String(),
	)
	file, err := os.OpenFile(run.reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		run.t.Errorf("create acceptance report: %v", err)
		return
	}
	if _, err := file.WriteString(report); err != nil {
		run.t.Errorf("write acceptance report: %v", err)
	}
	if err := file.Close(); err != nil {
		run.t.Errorf("close acceptance report: %v", err)
	}
}

func (run *savedMachineAcceptance) cleanupCall(ctx context.Context, arguments ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		output, err := run.call(ctx, nil, arguments...)
		if err == nil {
			return nil
		}
		failure := fmt.Errorf("%s: %w\n%s", strings.Join(arguments, " "), err, output)
		if !strings.Contains(output, "controller or endpoint is already reserved") {
			return failure
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), failure)
		case <-ticker.C:
		}
	}
}

func (run *savedMachineAcceptance) retireAcceptanceTask(ctx context.Context, id string) error {
	task, err := run.store.Read(ctx, id)
	if err != nil {
		return err
	}
	snapshot := task.Snapshot()
	if intent := snapshot.LaunchIntent; intent != nil {
		if err := run.cleanupCall(ctx, "backlog", "hold", id, "--reason", "acceptance cleanup"); err != nil {
			return err
		}
		if err := run.closeUnboundAcceptancePane(ctx, id, intent.Generation); err != nil {
			return err
		}
		if err := run.cleanupCall(ctx, "task", "abandon", id, "--generation", intent.Generation); err != nil {
			return err
		}
	} else if worker := snapshot.Worker; worker != nil {
		if snapshot.Completion == nil {
			if err := run.cleanupCall(ctx, "backlog", "hold", id, "--reason", "acceptance cleanup"); err != nil {
				return err
			}
		}
		if err := run.cleanupCall(ctx, "task", "stop", id, "--generation", worker.Generation); err != nil {
			return err
		}
		if snapshot.Completion == nil {
			if err := run.cleanupCall(ctx, "backlog", "finish", id, "--generation", worker.Generation, "--mode", "retain"); err != nil {
				return err
			}
		}
		if err := run.cleanupCall(ctx, "backlog", "cleanup", id, "--generation", worker.Generation); err != nil {
			return err
		}
	}
	task, err = run.store.Read(ctx, id)
	if err != nil {
		return err
	}
	snapshot = task.Snapshot()
	if snapshot.Worker != nil || snapshot.LaunchIntent != nil {
		return errors.New("acceptance task still has a worker or launch intent")
	}
	for _, receipt := range snapshot.AbandonedLaunches {
		if receipt.Lease.State == taskstate.LeaseHeld ||
			(receipt.Lease == (taskstate.WorktreeLease{}) && receipt.LeaseHolder != "") {
			if err := run.cleanupCall(ctx, "backlog", "release-worktree", id, "--generation", receipt.Generation); err != nil {
				return err
			}
		}
	}
	receipts := snapshot.PriorReceipts
	if snapshot.Receipt != nil {
		receipts = append(receipts, *snapshot.Receipt)
	}
	for _, receipt := range receipts {
		if receipt.Lease.State == taskstate.LeaseHeld ||
			receipt.RemoteResult.Lease.State == taskstate.LeaseHeld {
			if err := run.cleanupCall(ctx, "backlog", "release-worktree", id, "--generation", receipt.Generation); err != nil {
				return err
			}
		}
	}
	run.note("%s: acceptance cleanup confirms retired bindings and returned leases", id)
	return nil
}

func (run *savedMachineAcceptance) closeUnboundAcceptancePane(
	ctx context.Context,
	id, generation string,
) error {
	_, err := run.store.WithTask(ctx, id, func(locked *taskstate.LockedTask) error {
		current := locked.Current()
		if err := current.CheckAbandonInitialLaunch(generation); err != nil {
			return err
		}
		snapshot := current.Snapshot()
		intent := snapshot.LaunchIntent
		if snapshot.Worker != nil || intent == nil || intent.Endpoint == nil {
			return nil
		}
		target, err := herdr.ParseEndpoint(intent.Machine, intent.Session, intent.Endpoint.Pane)
		if err != nil {
			return err
		}
		return presentation.CloseBoundPane(
			ctx,
			herdr.NewClient("herdr"),
			target,
			intent.Endpoint.Workspace,
			intent.Endpoint.Tab,
			locked.CheckCurrent,
		)
	})
	return err
}

const removeSavedMachineAcceptanceDirectory = `set -eu
directory=$1
owner=$2
case "${directory%/*}" in /tmp|/private/tmp) ;; *) exit 1 ;; esac
case "${directory##*/}" in ca.??????) ;; *) exit 1 ;; esac
[ -d "$directory" ] && [ ! -L "$directory" ] || exit 1
[ "$(cd -P "$directory" && pwd)" = "$directory" ] || exit 1
[ -f "$directory/.acceptance-owner" ] && [ ! -L "$directory/.acceptance-owner" ] || exit 1
[ "$(cat "$directory/.acceptance-owner")" = "$owner" ] || exit 1
` + quietAcceptanceDirectory + `
rm -rf -- "$directory"
[ ! -e "$directory" ]
`

func TestSavedMachineAcceptancePathsStayInsideOwnedDirectory(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "owned", "acceptance")
	for _, test := range []struct {
		name, path string
		inside     bool
	}{
		{"worktree", filepath.Join(root, "work", "tree"), true},
		{"hidden child", filepath.Join(root, ".state"), true},
		{"root itself", root, false},
		{"parent", filepath.Dir(root), false},
		{"same prefix", root + "-other/tree", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := insideDirectory(root, test.path); got != test.inside {
				t.Fatalf("inside=%v want %v", got, test.inside)
			}
		})
	}
}

func TestSavedMachineAcceptanceDirectoryOnUnixPlatforms(t *testing.T) {
	for _, test := range []struct {
		name, directory string
		valid           bool
	}{
		{"macos", "/private/tmp/ca.Abc123", true},
		{"linux", "/tmp/ca.Abc123", true},
		{"relative", "tmp/ca.Abc123", false},
		{"other root", "/var/tmp/ca.Abc123", false},
		{"parent traversal", "/tmp/../tmp/ca.Abc123", false},
		{"child", "/private/tmp/ca.Abc123/child", false},
		{"short identity", "/private/tmp/ca.Abc12", false},
		{"newline", "/private/tmp/ca.Abc12\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validSavedMachineAcceptanceDirectory(test.directory); got != test.valid {
				t.Fatalf("directory %q accepted = %v, want %v", test.directory, got, test.valid)
			}
		})
	}
}
