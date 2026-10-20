package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/stateversion"
)

func TestPauseSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	root := initializedRoot(t)
	store := Store{Root: root}
	state, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || !state.Paused {
		t.Fatalf("migration enabled supervision: %+v", state)
	}
	if err := store.Enable(ctx, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, "operator maintenance"); err != nil {
		t.Fatal(err)
	}
	state, err = (Store{Root: root}).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || !state.Paused || state.PauseReason != "operator maintenance" {
		t.Fatalf("lost pause: %+v", state)
	}
}

func TestClaimExpiresAndAcknowledgementRequiresOutput(t *testing.T) {
	ctx := context.Background()
	root := initializedRoot(t)
	store := Store{Root: root}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	event, err := store.Stage(ctx, now, "snapshot", []string{"new-reply"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, event.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, event.ID, now); err == nil {
		t.Fatal("duplicate claim admitted")
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "", now); err == nil {
		t.Fatal("accepted missing output evidence")
	}
	retry, err := store.Claim(ctx, event.ID, now.Add(ClaimLease))
	if err != nil {
		t.Fatal(err)
	}
	if retry.Token == claim.Token {
		t.Fatal("claim token reused")
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "commentary", now.Add(ClaimLease)); err == nil {
		t.Fatal("accepted expired claim")
	}
	if err := store.Acknowledge(ctx, event.ID, retry.Token, "commentary:posted", now.Add(ClaimLease)); err != nil {
		t.Fatal(err)
	}
	state, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != nil || state.Receipt.Output != "commentary:posted" {
		t.Fatalf("missing receipt: %+v", state)
	}
	if err := store.Pause(ctx, "maintenance"); err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct {
		name, token, output string
		wantError           bool
	}{
		{"exact replay", retry.Token, "commentary:posted", false},
		{"wrong token", "wrong", "commentary:posted", true},
		{"changed output", retry.Token, "different", true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			err := store.Acknowledge(
				ctx,
				event.ID,
				trial.token,
				trial.output,
				now.Add(2*ClaimLease),
			)
			if (err != nil) != trial.wantError {
				t.Fatalf("ack error: %v", err)
			}
		})
	}
}

func TestPauseFencesActiveWork(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	event, err := store.Stage(ctx, now, "snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, event.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, "operator pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(ctx, now, "new", nil); err == nil {
		t.Fatal("staged during pause")
	}
	if _, err := store.Claim(ctx, event.ID, now); err == nil {
		t.Fatal("claimed during pause")
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "posted", now); err == nil {
		t.Fatal("acknowledged during pause")
	}
	if err := store.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(ctx, event.ID, claim.Token, "posted", now); err == nil {
		t.Fatal("old claim survived pause")
	}
	if _, err := store.Claim(ctx, event.ID, now); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{"supervisor.json", ".supervisor.lock"} {
		for _, kind := range []string{"symlink", "directory"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root := initializedRoot(t)
				target := filepath.Join(root, "target")
				original := `{"version":1,"paused":true}`
				if err := os.WriteFile(target, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, name)
				var err error
				if kind == "symlink" {
					err = os.Symlink("target", path)
				} else {
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := (Store{Root: root}).Read(context.Background()); err == nil {
					t.Fatal("accepted unsafe file")
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != original {
					t.Fatalf("target changed: %s, %v", data, err)
				}
			})
		}
	}
}

func initializedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	guard, _, err := stateversion.AcquireInitialization(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := guard.PublishInitialVersion(); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestOversizedMutationPreservesState(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.Enable(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(ctx, now, strings.Repeat("x", 2<<20), nil); err == nil {
		t.Fatal("accepted oversized snapshot")
	}
	state, err := store.Read(ctx)
	if err != nil || state.Pending != nil {
		t.Fatalf("previous state damaged: %+v, %v", state, err)
	}
}
