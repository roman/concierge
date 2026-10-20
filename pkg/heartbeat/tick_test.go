package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman/tincan/pkg/inbox"
)

func TestTickChecksDeadlineBeforeReadingStatus(t *testing.T) {
	ctx := context.Background()
	root := initializedRoot(t)
	store := Store{Root: root}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, func(state *State) error { state.Next = now.Add(time.Minute); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "operator-actions.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	recipient, _ := inbox.ParseRecipient("concierge")
	messageRoot := t.TempDir()
	if _, err := Tick(ctx, root, messageRoot, recipient, "/unused", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Tick(ctx, root, messageRoot, recipient, "/unused", now.Add(time.Minute)); err == nil {
		t.Fatal("due tick did not collect fresh status")
	}
}
