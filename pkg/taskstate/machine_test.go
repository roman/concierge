package taskstate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roman/concierge/pkg/stateversion"
)

func TestRemoteWorkerRequiresUpgradedRoot(t *testing.T) {
	root := versionTwoRoot(t)
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), queuedStoreTask(t)); err != nil {
		t.Fatal(err)
	}
	worker := workerForTransition()
	worker.Machine = "machine-1"
	if _, err := store.ReserveAdoption(context.Background(), "task-1", worker); !errors.Is(
		err,
		stateversion.ErrIncompatible,
	) {
		t.Fatalf("remote worker in old root: %v", err)
	}
	current, err := store.Read(context.Background(), "task-1")
	if err != nil || current.Snapshot().Worker != nil {
		t.Fatalf("failed reservation changed task: %+v %v", current.Snapshot(), err)
	}
	if err := UpgradeVersionTwo(context.Background(), root, root+"-backup"); err != nil {
		t.Fatal(err)
	}
	current, err = store.ReserveAdoption(context.Background(), "task-1", worker)
	if err != nil {
		t.Fatal(err)
	}
	data, err := current.Encode()
	if err != nil || !bytes.Contains(data, []byte(`"format":3`)) {
		t.Fatalf("remote encoding: %s %v", data, err)
	}
	decoded, err := Decode(data)
	if err != nil || decoded.Snapshot().Worker.Machine != worker.Machine {
		t.Fatalf("remote roundtrip: %+v %v", decoded.Snapshot(), err)
	}
	if _, err := Decode(bytes.Replace(data, []byte(`"format":3`), []byte(`"format":2`), 1)); err == nil {
		t.Fatal("version two record accepted a remote endpoint")
	}
}

func TestWorkerReservationsScopeEndpointAndPathByMachine(t *testing.T) {
	for _, machine := range []string{"machine-1", "machine-2", ""} {
		t.Run(machine, func(t *testing.T) {
			root := versionTwoRoot(t)
			store, err := NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"task-1", "task-2"} {
				task, err := NewQueued(
					Definition{ID: id, Title: "Work", Repo: "concierge", Kind: "task"},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Create(context.Background(), task); err != nil {
					t.Fatal(err)
				}
			}
			if err := UpgradeVersionTwo(context.Background(), root, root+"-backup"); err != nil {
				t.Fatal(err)
			}
			worker := workerForTransition()
			worker.Machine = "machine-1"
			if _, err := store.ReserveAdoption(context.Background(), "task-1", worker); err != nil {
				t.Fatal(err)
			}
			worker.Machine = machine
			_, err = store.ReserveAdoption(context.Background(), "task-2", worker)
			if machine == "machine-1" && !errors.Is(err, ErrWorkerConflict) {
				t.Fatalf("duplicate endpoint: %v", err)
			}
			if machine != "machine-1" && err != nil {
				t.Fatalf("independent machine rejected: %v", err)
			}
		})
	}
}

func TestRemoteRecordCannotBeReadThroughOldRoot(t *testing.T) {
	root := versionTwoRoot(t)
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), queuedStoreTask(t)); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeVersionTwo(context.Background(), root, root+"-backup"); err != nil {
		t.Fatal(err)
	}
	worker := workerForTransition()
	worker.Machine = "machine-1"
	if _, err := store.ReserveAdoption(context.Background(), "task-1", worker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".task-state-version"), []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background(), "task-1"); !errors.Is(
		err,
		stateversion.ErrIncompatible,
	) {
		t.Fatalf("read: %v", err)
	}
	if _, err := store.List(context.Background()); !errors.Is(err, stateversion.ErrIncompatible) {
		t.Fatalf("list: %v", err)
	}
	_, err = store.WithTask(
		context.Background(),
		"task-1",
		func(*LockedTask) error { t.Fatal("callback entered an incompatible record"); return nil },
	)
	if !errors.Is(err, stateversion.ErrIncompatible) {
		t.Fatalf("locked operation: %v", err)
	}
}

func TestLaunchIntentPreservesMachineAndRejectsDifferentBinding(t *testing.T) {
	intent := launchForTest()
	intent.Machine = "machine-1"
	intent.Phase = LaunchAgentRequested
	intent.Endpoint = &LaunchEndpoint{Workspace: "workspace-1", Tab: "tab-1", Pane: "pane-1"}
	worker := workerForTransition()
	worker.Machine, worker.Workspace, worker.Tab = intent.Machine, intent.Endpoint.Workspace, intent.Endpoint.Tab
	intent.ResumeArgs = worker.AgentArgs
	snapshot := taskForTransition(t).Snapshot()
	snapshot.State, snapshot.Worker, snapshot.LaunchIntent = InFlight, &worker, &intent
	task, err := NewMigrated(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || decoded.Snapshot().LaunchIntent.Machine != intent.Machine {
		t.Fatalf("launch machine: %+v %v", decoded.Snapshot(), err)
	}
	worker.Machine = "machine-2"
	if _, err := NewMigrated(snapshot); err == nil {
		t.Fatal("launch bound a worker on a different machine")
	}
}
