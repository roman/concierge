package hostactivity

import (
	"errors"
	"os"
	"testing"
)

func TestConcurrentBeginCreatesSharedLease(t *testing.T) {
	for range 30 {
		root := t.TempDir()
		start := make(chan struct{})
		type result struct {
			lease *os.File
			err   error
		}
		results := make(chan result, 16)
		for range cap(results) {
			go func() {
				<-start
				lease, err := Begin(root)
				results <- result{lease, err}
			}()
		}
		close(start)
		var leases []*os.File
		var acquisitionError error
		for range cap(results) {
			outcome := <-results
			if outcome.err != nil {
				acquisitionError = outcome.err
			}
			if outcome.lease != nil {
				leases = append(leases, outcome.lease)
			}
		}
		for _, lease := range leases {
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		}
		if acquisitionError != nil {
			t.Fatalf("concurrent shared lease: %v", acquisitionError)
		}
	}
}

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
