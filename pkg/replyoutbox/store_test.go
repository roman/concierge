package replyoutbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func privateOutbox(t *testing.T) (string, *Store) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	return directory, store
}

func TestOutboxRetainsOriginalPayloadAndPartialReceiptsAcrossReload(t *testing.T) {
	directory, store := privateOutbox(t)
	payload := payloadFixture()
	ctx := context.Background()
	if _, err := store.Stage(ctx, payload); err != nil {
		t.Fatal(err)
	}
	id := "k-" + strings.Repeat("b", 64)
	first, err := store.Confirm(ctx, payload, Progress{MessageID: id, PublicationConfirmed: true})
	if err != nil || first.Complete() {
		t.Fatalf("publication alone completed delivery: %+v %v", first.Progress, err)
	}
	reloaded, _ := New(directory)
	retained, err := reloaded.Stage(ctx, payload)
	if err != nil || retained.Payload != payload || retained.Progress != first.Progress {
		t.Fatalf("reload lost original reply or receipts: %+v %v", retained.Progress, err)
	}
	second, err := reloaded.Confirm(ctx, payload, Progress{ArchiveConfirmed: true})
	if err != nil || second.Complete() {
		t.Fatalf("archive alone completed delivery: %+v %v", second.Progress, err)
	}
	final, err := reloaded.Confirm(ctx, payload, Progress{AssignmentAcknowledged: true})
	if err != nil || !final.Complete() || final.Payload != payload {
		t.Fatalf(
			"separate confirmations did not complete original reply: %+v %v",
			final.Progress,
			err,
		)
	}
	path, _ := store.Path(payload)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("retained reply is absent or public: %v", err)
	}
}

func TestOutboxRefusesAChangedBodyOrKeyAndRetainsRevokedReplies(t *testing.T) {
	directory, store := privateOutbox(t)
	payload := payloadFixture()
	ctx := context.Background()
	if _, err := store.Stage(ctx, payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"body", "key"} {
		changed := payload
		if field == "body" {
			changed.Body = "Different result"
		} else {
			changed.Key = "other-key"
		}
		if _, err := store.Stage(ctx, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed %s replaced pending original: %v", field, err)
		}
	}
	if _, err := store.Revoke(ctx, payload); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := New(directory)
	record, err := reloaded.Stage(ctx, payload)
	if !errors.Is(err, ErrRevoked) || record.Payload != payload || !record.Revoked {
		t.Fatalf("revoked reply was lost or resumed: %+v %v", record.Progress, err)
	}
	if _, err := reloaded.Confirm(ctx, payload, Progress{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked reply accepted a later retry: %v", err)
	}
}

func TestOutboxRefusesForeignPathsBeforeReplacingThem(t *testing.T) {
	for _, kind := range []string{"public directory", "symlink directory", "symlink record"} {
		t.Run(kind, func(t *testing.T) {
			directory, store := privateOutbox(t)
			other := t.TempDir()
			payload := payloadFixture()
			if kind == "public directory" {
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			} else if kind == "symlink directory" {
				if err := os.Symlink(other, filepath.Join(directory, "outbox")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(directory, "outbox"), 0700); err != nil {
					t.Fatal(err)
				}
				path, _ := store.Path(payload)
				if err := os.Symlink(filepath.Join(other, "retain"), path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Stage(context.Background(), payload); !errors.Is(err, ErrStorage) {
				t.Fatalf("foreign path accepted: %v", err)
			}
			entries, err := os.ReadDir(other)
			if err != nil || len(entries) != 0 {
				t.Fatalf("foreign destination changed: %v", err)
			}
		})
	}
}

func TestOutboxRetryConfirmsAnUncertainDirectoryPublication(t *testing.T) {
	directory, store := privateOutbox(t)
	payload := payloadFixture()
	flush := store.syncDirectory
	failed, unconfirmed := false, false
	store.syncDirectory = func(root *os.Root) error {
		selected, err := os.Stat(filepath.Join(directory, "outbox"))
		opened, statError := root.Stat(".")
		if err == nil && statError == nil && os.SameFile(selected, opened) {
			if !failed {
				failed, unconfirmed = true, true
				return ErrStorage
			}
			unconfirmed = false
		}
		return flush(root)
	}
	if _, err := store.Stage(context.Background(), payload); !errors.Is(err, ErrStorage) ||
		!unconfirmed {
		t.Fatalf("directory sync failure was not retained: %v", err)
	}
	if _, err := store.Stage(context.Background(), payload); err != nil || unconfirmed {
		t.Fatalf("retry reported durability without confirming publication: %v", err)
	}
}

func TestOutboxConcurrentConfirmationsCannotLoseProgress(t *testing.T) {
	_, store := privateOutbox(t)
	payload := payloadFixture()
	ctx := context.Background()
	if _, err := store.Stage(ctx, payload); err != nil {
		t.Fatal(err)
	}
	id := "k-" + strings.Repeat("b", 64)
	var group sync.WaitGroup
	failures := make(chan error, 3)
	for _, progress := range []Progress{
		{MessageID: id, PublicationConfirmed: true},
		{MessageID: id, PublicationConfirmed: true, ArchiveConfirmed: true},
		{MessageID: id, PublicationConfirmed: true, ArchiveConfirmed: true, AssignmentAcknowledged: true},
	} {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.Confirm(ctx, payload, progress)
			failures <- err
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.Stage(ctx, payload)
	if err != nil || !record.Complete() || record.Progress.MessageID != id {
		t.Fatalf("concurrent confirmation lost a receipt: %+v %v", record.Progress, err)
	}
	if _, err := store.Confirm(ctx, payload, Progress{MessageID: "k-" + strings.Repeat("c", 64), PublicationConfirmed: true}); !errors.Is(
		err,
		ErrConflict,
	) {
		t.Fatalf("receipt from another reply replaced the original identity: %v", err)
	}
}
