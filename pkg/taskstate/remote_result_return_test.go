package taskstate

import (
	"strings"
	"testing"
)

func TestPrimaryResultReturnPreservesBothLeaseReceipts(t *testing.T) {
	for _, scope := range []string{"latest", "prior"} {
		t.Run(scope, func(t *testing.T) {
			task := remoteResultTask(t)
			commit := strings.Repeat("c", 40)
			var err error
			task, err = task.SelectRemoteResult("gen-1", commit, "primary-holder")
			if err != nil {
				t.Fatal(err)
			}
			task, err = task.RequestRemoteResultLease("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			primary := WorktreeLease{
				ID:         "primary-lease",
				Holder:     "primary-holder",
				Project:    "/primary/repo",
				Path:       "/primary/pool/slot/repo",
				Branch:     "feat/task",
				BaseCommit: commit,
				State:      LeaseHeld,
			}
			task, err = task.RecordRemoteResultLease("gen-1", primary)
			if err != nil {
				t.Fatal(err)
			}
			task, err = task.ConfirmRemoteResult("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := task.RecordPrimaryResultLeaseReturned("gen-1", primary); err == nil {
				t.Fatal("returned an active delivery lease")
			}
			task, err = task.PlaceHold(Hold{Reason: "Follow-up", Kind: "primary"})
			if err != nil {
				t.Fatal(err)
			}
			task, err = task.AcceptResult("gen-1", Retain, "")
			if err != nil {
				t.Fatal(err)
			}
			task, err = task.RetireResult("gen-1")
			if err != nil {
				t.Fatal(err)
			}
			if scope == "prior" {
				snapshot := task.Snapshot()
				snapshot.PriorReceipts = []Receipt{*snapshot.Receipt}
				snapshot.Receipt = &Receipt{Generation: "latest-generation", Mode: Retain}
				task, err = NewMigrated(snapshot)
				if err != nil {
					t.Fatal(err)
				}
			}
			wrong := primary
			wrong.ID = "foreign-lease"
			if _, err := task.RecordPrimaryResultLeaseReturned("gen-1", wrong); err == nil {
				t.Fatal("returned a foreign primary lease")
			}
			if _, err := task.RecordPrimaryResultLeaseReturned("wrong-generation", primary); err == nil {
				t.Fatal("returned another generation's primary lease")
			}
			for range 2 {
				task, err = task.RecordPrimaryResultLeaseReturned("gen-1", primary)
				if err != nil {
					t.Fatal(err)
				}
			}
			result, found := task.RetiredRemoteResult("gen-1")
			if !found || result.Commit != commit || result.Lease.State != LeaseReturned {
				t.Fatal("lost the selected commit or primary return receipt")
			}
			remote, found := task.RetiredLease("gen-1")
			if !found || remote.State != LeaseHeld {
				t.Fatal("primary return changed the native lease receipt")
			}
			task, err = task.RecordLeaseReturned("gen-1", remote)
			if err != nil {
				t.Fatal(err)
			}
			data, err := task.Encode()
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			result, _ = loaded.RetiredRemoteResult("gen-1")
			remote, _ = loaded.RetiredLease("gen-1")
			if result.Lease.State != LeaseReturned || remote.State != LeaseReturned {
				t.Fatal("reload lost independent return confirmations")
			}
		})
	}
}
