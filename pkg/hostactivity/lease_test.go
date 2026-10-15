package hostactivity

import (
	"errors"
	"testing"
)

func TestQuiesceExcludesLaunchesAndLandings(t *testing.T) {
	root := t.TempDir()
	first, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := Quiesce(root); !errors.Is(err, ErrBusy) {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("quiesce admitted active work: %v", err)
	}
	first.Close()
	second.Close()
	exclusive, err := Quiesce(root)
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := Begin(root); !errors.Is(err, ErrBusy) {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("work admitted during shutdown: %v", err)
	}
	exclusive.Close()
	lease, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}
