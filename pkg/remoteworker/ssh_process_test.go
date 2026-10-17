package remoteworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/roman/concierge/pkg/primary"
)

func TestSSHProcessRejectsConfiguredForwardsBeforeStarting(t *testing.T) {
	root, spec := tunnelFixture(t)
	inspector := &secretRunner{stdout: []byte("hostname machine\nlocalforward 8080 localhost:80\n")}
	driver := SSHProcesses{Executable: "/missing/ssh", Inspector: inspector}
	_, err := driver.Start(context.Background(), spec, filepath.Join(root, "worker.sock"))
	if !errors.Is(err, ErrTunnelNotStarted) || !errors.Is(err, ErrTunnelConfiguration) {
		t.Fatalf("configured forward did not prevent process creation: %v", err)
	}
	if !slices.Contains(inspector.arguments, "-G") || slices.Contains(inspector.arguments, "-R") {
		t.Fatal("configuration inspection tried to establish a forward")
	}
}

func TestSSHProcessSurvivesCallerCancellationAndStopsOnlyOwnedIncarnation(t *testing.T) {
	root, spec := tunnelFixture(t)
	executable := filepath.Join(root, "fake-ssh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	driver := SSHProcesses{Executable: executable,
		Inspector: &secretRunner{stdout: []byte("hostname synthetic-machine\n")}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token, err := driver.Start(ctx, spec, filepath.Join(root, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := driver.Stop(context.Background(), token); err != nil {
			t.Error(err)
		}
	}()
	cancel()
	status, err := driver.Status(token)
	if err != nil || status != primary.ProcessRunning {
		t.Fatalf("caller cancellation killed the tunnel: %q %v", status, err)
	}
	other := token
	other.StartTicks += "-other"
	if err := driver.Stop(context.Background(), other); err != nil {
		t.Fatalf("an exited original incarnation should need no stop: %v", err)
	}
	status, err = driver.Status(token)
	if err != nil || status != primary.ProcessRunning {
		t.Fatalf("another incarnation was stopped: %q %v", status, err)
	}
	if err := driver.Stop(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	status, err = driver.Status(token)
	if err != nil || status != primary.ProcessExited {
		t.Fatalf("owned tunnel remains after stop: %q %v", status, err)
	}
}
