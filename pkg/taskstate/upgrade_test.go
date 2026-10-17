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

func TestUpgradeVersionTwoBacksUpAndPreservesRecords(t *testing.T) {
	root := versionTwoRoot(t)
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), queuedStoreTask(t)); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "records", "task-1.json")
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	backup := root + "-backup"
	if err := UpgradeVersionTwo(context.Background(), root, backup); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{record, filepath.Join(backup, "records", "task-1.json")} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, before) {
			t.Fatalf("record %s changed: %v", path, err)
		}
	}
	if guard, err := stateversion.AcquireVersionTwo(context.Background(), root); err == nil {
		guard.Close()
		t.Fatal("old client accepted upgraded state")
	}
	if _, err := store.Update(context.Background(), "task-1", func(task Task) (Task, error) {
		return task.PlaceHold(Hold{Reason: "Awaiting input"})
	}); err != nil {
		t.Fatalf("local transition after upgrade: %v", err)
	}
	guard, err := stateversion.AcquireCurrent(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	tasks, err := ListPinned(context.Background(), guard.StateRoot())
	if err != nil || len(tasks) != 1 || tasks[0].ID() != "task-1" {
		t.Fatalf("upgraded records: %+v, %v", tasks, err)
	}
}

func TestUpgradeRecoveryRejectsChangedRecord(t *testing.T) {
	root := versionTwoRoot(t)
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), queuedStoreTask(t)); err != nil {
		t.Fatal(err)
	}
	backup := root + "-backup"
	if err := UpgradeVersionTwo(context.Background(), root, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".task-state-migration-pending"), []byte("3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "records", "task-1.json")
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, bytes.Replace(data, []byte(`"Work"`), []byte(`"Changed"`), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverVersionTwoUpgrade(context.Background(), root, backup); err == nil {
		t.Fatal("recovery accepted changed task record")
	}
	if _, err := os.Stat(filepath.Join(root, ".task-state-migration-pending")); err != nil {
		t.Fatalf("failed recovery removed pending marker: %v", err)
	}
	if err := os.WriteFile(record, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverVersionTwoUpgrade(context.Background(), root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".task-state-migration-pending")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("successful recovery retained pending marker: %v", err)
	}
}

func TestUpgradeRefusesExistingWorker(t *testing.T) {
	root := versionTwoRoot(t)
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), queuedStoreTask(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveAdoption(context.Background(), "task-1", workerForTransition()); err != nil {
		t.Fatal(err)
	}
	backup := root + "-backup"
	if err := UpgradeVersionTwo(context.Background(), root, backup); err == nil {
		t.Fatal("upgrade would make the existing worker client incompatible")
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused upgrade created a backup: %v", err)
	}
	if _, err := store.Read(context.Background(), "task-1"); err != nil {
		t.Fatalf("refused upgrade changed the version: %v", err)
	}
}
