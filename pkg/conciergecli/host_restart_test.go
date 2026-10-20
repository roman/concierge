package conciergecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/supervise"
)

func TestReplacementPreflightRejectsMissingHerdr(t *testing.T) {
	root := t.TempDir()
	for _, tool := range []string{"tincan", "git", "treehouse"} {
		if err := os.WriteFile(filepath.Join(root, tool), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	missingHerdr := filepath.Join(root, "missing-herdr")
	_, log, err := replacementHost(&hostservice.StatusResult{
		Root: root, HerdrExecutable: missingHerdr,
		Supervision: supervise.Config{Interval: time.Second, RestartWindow: time.Minute},
	})
	if log != nil {
		log.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "missing-herdr") {
		t.Fatalf("preflight accepted a missing Herdr executable: %v", err)
	}
}

func TestHostRestartUpgradesBinary(t *testing.T) {
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("TINCAN_HERDR", "")
	root := shortTaskRoot(t)
	directory := t.TempDir()
	for _, tool := range []string{"tincan", "treehouse"} {
		if err := os.WriteFile(filepath.Join(directory, tool), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	binaries := make(map[string]string)
	for _, sourceRevision := range []string{"revision-a", "revision-b"} {
		binary := filepath.Join(directory, sourceRevision)
		build := exec.Command(
			"go",
			"build",
			"-ldflags",
			"-X github.com/roman/concierge/pkg/conciergecli.revision="+sourceRevision,
			"-o",
			binary,
			"../../cmd/concierge",
		)
		if data, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %s, %v", data, err)
		}
		binaries[sourceRevision] = binary
	}
	args := []string{
		"--root",
		root,
		"--message-root",
		filepath.Join(root, "messages"),
		"host",
		"--session",
		"work",
	}
	old := exec.Command(binaries["revision-a"], append(args, "--herdr", unusedHerdr(t), "serve")...)
	log, err := os.Create(filepath.Join(directory, "old.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	old.Stdout, old.Stderr = log, log
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Process.Kill(); _ = old.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		response, err := hostservice.Call(
			ctx,
			filepath.Join(root, "host.sock"),
			hostservice.Request{
				Operation: hostservice.HostStatus,
				Status:    &hostservice.StatusRequest{},
			},
		)
		if err == nil && response.Outcome == hostservice.Completed {
			break
		}
		if ctx.Err() != nil {
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("host readiness: %v; log: %s", ctx.Err(), data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := func(binary string, operation ...string) string {
		t.Helper()
		data, err := exec.CommandContext(ctx, binary, append(args, operation...)...).
			CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s, %v", operation, data, err)
		}
		return string(data)
	}
	before := run(binaries["revision-b"], "status")
	for _, expected := range []string{"host_revision: \"revision-a\"", "binary_revision: \"revision-b\"", "upgrade_required: yes"} {
		if !strings.Contains(before, expected) {
			t.Fatalf("missing %s: %s", expected, before)
		}
	}
	activity, err := hostactivity.Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := exec.CommandContext(ctx, binaries["revision-b"], append(args, "restart")...).
		CombinedOutput()
	activity.Close()
	if err == nil || !strings.Contains(string(refused), "in progress") {
		t.Fatalf("restart admitted active work: %s, %v", refused, err)
	}
	if status := run(binaries["revision-b"], "status"); !strings.Contains(
		status,
		"host_revision: \"revision-a\"",
	) {
		t.Fatalf("refused restart replaced the host: %s", status)
	}
	// Always stop the detached replacement, including after an assertion fails.
	t.Cleanup(func() {
		stop := exec.Command(binaries["revision-b"], append(args, "stop")...)
		if data, err := stop.CombinedOutput(); err != nil {
			t.Errorf("cleanup: %s, %v", data, err)
		}
	})
	result := run(binaries["revision-b"], "restart")
	if !strings.Contains(result, "status: restarted") {
		t.Fatal(result)
	}
	after := run(binaries["revision-b"], "status")
	for _, expected := range []string{"host_revision: \"revision-b\"", "binary_revision: \"revision-b\"", "upgrade_required: no", "session: \"work\""} {
		if !strings.Contains(after, expected) {
			t.Fatalf("missing %s: %s", expected, after)
		}
	}
	for _, step := range []struct {
		name      string
		arguments []string
		policy    string
	}{
		{"enter maintenance", []string{"restart", "--recovery", "maintenance"}, "maintenance"},
		{"preserve maintenance", []string{"restart"}, "maintenance"},
		{"restore normal", []string{"restart", "--recovery", "normal"}, "normal"},
	} {
		run(binaries["revision-b"], step.arguments...)
		status := run(binaries["revision-b"], "status")
		if !strings.Contains(status, "recovery: \""+step.policy+"\"") {
			t.Fatalf("%s: %s", step.name, status)
		}
		response, err := hostservice.Call(
			ctx,
			filepath.Join(root, "host.sock"),
			hostservice.Request{
				Operation: hostservice.HostStatus,
				Status:    &hostservice.StatusRequest{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if response.Status.Session != "work" ||
			response.Status.Supervision != (supervise.Config{Interval: 5 * time.Second, RestartTolerance: 5, RestartWindow: time.Minute}) {
			t.Fatalf("%s changed host settings: %+v", step.name, response.Status)
		}
	}
}

func TestReplacementPreservesReportedRecovery(t *testing.T) {
	for _, policy := range []hostservice.RecoveryPolicy{"", hostservice.RecoveryNormal, hostservice.RecoveryMaintenance} {
		t.Run(string(policy), func(t *testing.T) {
			root := t.TempDir()
			status := &hostservice.StatusResult{
				Root: root, MessageRoot: filepath.Join(root, "messages"), Recipient: "concierge", Session: "work",
				HerdrExecutable: unusedHerdr(t), Recovery: policy,
				Supervision: supervise.Config{
					Interval:         17 * time.Second,
					RestartTolerance: 3,
					RestartWindow:    2 * time.Minute,
				},
			}
			for _, tool := range []string{"tincan", "git", "treehouse"} {
				if err := os.WriteFile(filepath.Join(root, tool), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", root)
			process, log, err := replacementHost(status)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			arguments := strings.Join(process.Args[1:], " ")
			for _, expected := range []string{"--recovery " + string(policy.Effective()), "--session work", "--interval 17s", "--restart-tolerance 3", "--restart-window 2m0s"} {
				if !strings.Contains(arguments, expected) {
					t.Fatalf("missing %q: %s", expected, arguments)
				}
			}
		})
	}
}

func TestReplacementReadinessRequiresSelectedPolicy(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		reported  hostservice.RecoveryPolicy
		selected  hostservice.RecoveryPolicy
		wantError bool
	}{
		{"maintenance", hostservice.RecoveryMaintenance, hostservice.RecoveryMaintenance, false},
		{"normal", hostservice.RecoveryNormal, hostservice.RecoveryNormal, false},
		{"legacy normal", "", hostservice.RecoveryNormal, false},
		{"maintenance lost", hostservice.RecoveryNormal, hostservice.RecoveryMaintenance, true},
		{"maintenance unchanged", hostservice.RecoveryMaintenance, hostservice.RecoveryNormal, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := &hostservice.StatusResult{
				Executable: executable,
				Revision:   revision,
				Recovery:   test.reported,
			}
			if err := checkReplacementStatus(status, test.selected); (err != nil) != test.wantError {
				t.Fatalf("replacement policy readiness: %v", err)
			}
		})
	}
}
