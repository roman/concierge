package stateversion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeVersionThreeExcludesVersionTwoCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, versionFile), []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireUpgrade(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.PublishVersion(3); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if guard, err := AcquireVersionTwo(context.Background(), root); err == nil {
		guard.Close()
		t.Fatal("version two command accepted upgraded state")
	}
	current, err := AcquireCurrent(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	current.Close()
}

func TestUpgradeRecoveryPreservesPublicationBoundary(t *testing.T) {
	for _, version := range []string{"2\n", "3\n"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range map[string]string{versionFile: version, pendingFile: "3\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := AcquireCurrent(context.Background(), root); !errors.Is(
				err,
				ErrIncompatible,
			) {
				t.Fatalf("command entered pending upgrade: %v", err)
			}
			if guard, err := AcquireRecovery(context.Background(), root); err == nil {
				guard.Close()
				t.Fatal("legacy recovery accepted a version three upgrade")
			}
			guard, err := AcquireUpgradeRecovery(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := guard.PublishVersion(2); !errors.Is(err, ErrIncompatible) {
				t.Fatalf("recovery changed upgrade target: %v", err)
			}
			if err := guard.PublishVersion(3); err != nil {
				t.Fatal(err)
			}
			guard.Close()
			current, err := AcquireCurrent(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			current.Close()
		})
	}
}

func TestCurrentGuardPreservesVersionTwoCompatibility(t *testing.T) {
	for _, version := range []string{"1\n", "2\n", "3\n", "4\n"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, versionFile), []byte(version), 0600); err != nil {
				t.Fatal(err)
			}
			guard, err := AcquireCurrent(context.Background(), root)
			want := version == "2\n" || version == "3\n"
			if (err == nil) != want {
				t.Fatalf("current command version %q: %v", version, err)
			}
			if guard != nil {
				guard.Close()
			}
		})
	}
}
