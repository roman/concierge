package taskmeta

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLockInputs(t *testing.T) (AbsolutePath, TaskID, string) {
	t.Helper()
	state, err := ParseAbsolutePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := ParseTaskID("task-1")
	if err != nil {
		t.Fatal(err)
	}
	return state, id, filepath.Join(state.String(), ".meta-task-1.lock")
}

func TestTaskLockLiveContentionAndRelease(t *testing.T) {
	state, id, path := testLockInputs(t)
	first, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(path)
	if err != nil || filepath.Dir(target) != state.String() {
		t.Fatalf("owner target = %q, %v", target, err)
	}
	pid, err := os.ReadFile(filepath.Join(target, "pid"))
	if err != nil || string(pid) != strconv.Itoa(os.Getpid())+"\n" {
		t.Fatalf("owner pid = %q, %v", pid, err)
	}
	if _, err := AcquireTaskLock(state, id, 50*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("contended acquire = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released link remains: %v", err)
	}
	second, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockReclaimsDeadOwner(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy bool
	}{
		{name: "symlink owner"},
		{name: "legacy directory", legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, id, path := testLockInputs(t)
			owner := path
			if !test.legacy {
				owner = path + ".owner.stale"
			}
			if err := os.Mkdir(owner, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(owner, "pid"), []byte("99999999\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if !test.legacy {
				if err := os.Symlink(owner, path); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := AcquireTaskLock(state, id, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTaskLockStealMarkerBlocksClaim(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "regular file",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "live owner symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				owner := path + ".owner.live"
				if err := os.Mkdir(owner, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(owner, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(owner, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Symlink(path+".owner.missing", path); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, id, path := testLockInputs(t)
			test.setup(t, path+".steal")
			if _, err := AcquireTaskLock(state, id, 50*time.Millisecond); !errors.Is(
				err,
				ErrLockTimeout,
			) {
				t.Fatalf("steal marker acquire = %v", err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("claim survived steal marker: %v", err)
			}
		})
	}
}

func TestTaskLockOwnerSymlinkCannotDeleteVictim(t *testing.T) {
	state, id, _ := testLockInputs(t)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(state.String(), "victim")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"pid":  strconv.Itoa(os.Getpid()) + "\n",
		"role": "keep this file",
	} {
		if err := os.WriteFile(filepath.Join(victim, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(lock.ownerPath, "pid")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lock.ownerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, lock.ownerPath); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); !errors.Is(err, ErrNotLockOwner) {
		t.Fatalf("release with swapped owner = %v", err)
	}
	for _, name := range []string{"pid", "role"} {
		if _, err := os.Stat(filepath.Join(victim, name)); err != nil {
			t.Fatalf("victim %s changed: %v", name, err)
		}
	}
}

func TestTaskLockSerializesGoroutines(t *testing.T) {
	state, id, _ := testLockInputs(t)
	var active, maximum atomic.Int32
	const workers = 12
	var group sync.WaitGroup
	errorsSeen := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			lock, err := AcquireTaskLock(state, id, 3*time.Second)
			if err != nil {
				errorsSeen <- err
				return
			}
			now := active.Add(1)
			for {
				previous := maximum.Load()
				if now <= previous || maximum.CompareAndSwap(previous, now) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			if err := lock.Release(); err != nil {
				errorsSeen <- err
			}
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent holders = %d", maximum.Load())
	}
}

func TestTaskLockConcurrentRelease(t *testing.T) {
	state, id, _ := testLockInputs(t)
	for range 100 {
		lock, err := AcquireTaskLock(state, id, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		copyOfLock := *lock
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, holder := range []*TaskLock{lock, &copyOfLock} {
			go func() {
				<-start
				results <- holder.Release()
			}()
		}
		close(start)
		first, second := <-results, <-results
		oneSuccess := (first == nil && errors.Is(second, ErrNotLockOwner)) ||
			(second == nil && errors.Is(first, ErrNotLockOwner))
		if !oneSuccess {
			t.Fatalf("concurrent releases = %v, %v", first, second)
		}
	}
}

func TestTaskLockMissingPIDGrace(t *testing.T) {
	for _, test := range []struct {
		name string
		pid  string
	}{
		{name: "missing"},
		{name: "malformed", pid: "not-a-pid\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, id, path := testLockInputs(t)
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if test.pid != "" {
				if err := os.WriteFile(filepath.Join(path, "pid"), []byte(test.pid), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := AcquireTaskLock(state, id, 50*time.Millisecond); !errors.Is(
				err,
				ErrLockTimeout,
			) {
				t.Fatalf("fresh invalid-PID lock = %v", err)
			}
			old := time.Now().Add(-3 * time.Second)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			lock, err := AcquireTaskLock(state, id, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func createDeadLink(t *testing.T, path string) string {
	t.Helper()
	owner := path + ".owner.dead"
	if err := os.Mkdir(owner, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "pid"), []byte("99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owner, path); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestTaskLockReclaimsStaleSteal(t *testing.T) {
	state, id, path := testLockInputs(t)
	createDeadLink(t, path)
	steal := path + ".steal"
	createDeadLink(t, steal)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockReclaimsPrimaryTombstone(t *testing.T) {
	state, id, path := testLockInputs(t)
	owner := createDeadLink(t, path)
	if err := os.Rename(owner, owner+".reaped.99999999"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockStaleSymlinkOwnerPreservesVictim(t *testing.T) {
	state, id, path := testLockInputs(t)
	victim := filepath.Join(state.String(), "victim")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "pid"), []byte("99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := path + ".owner.malicious"
	if err := os.Symlink(victim, owner); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owner, path); err != nil {
		t.Fatal(err)
	}
	_, err := AcquireTaskLock(state, id, 50*time.Millisecond)
	if err == nil {
		t.Fatal("malicious owner was acquired")
	}
	if _, err := os.Stat(filepath.Join(victim, "pid")); err != nil {
		t.Fatalf("victim PID changed: %v", err)
	}
}

func TestTaskLockConcurrentStaleReclaim(t *testing.T) {
	state, id, path := testLockInputs(t)
	createDeadLink(t, path)
	const workers = 8
	var group sync.WaitGroup
	results := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			lock, err := AcquireTaskLock(state, id, 3*time.Second)
			if err != nil {
				results <- err
				return
			}
			time.Sleep(time.Millisecond)
			results <- lock.Release()
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestTaskLockPreservesSuccessorLink(t *testing.T) {
	_, _, path := testLockInputs(t)
	createDeadLink(t, path)
	stale, err := observeLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	successorOwner := path + ".owner.successor"
	if err := os.Mkdir(successorOwner, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(successorOwner, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(successorOwner, path); err != nil {
		t.Fatal(err)
	}
	if stale.unchanged() || sameLink(stale) {
		t.Fatal("stale snapshot accepted successor link")
	}
	steal, err := tryCreateTaskLock(path+".steal", nil)
	if err != nil || steal == nil {
		t.Fatalf("claim steal lock = %+v, %v", steal, err)
	}
	recovered, err := reclaimUnderSteal(path, steal)
	if err != nil || recovered != nil {
		t.Fatalf("reclaim successor = %+v, %v", recovered, err)
	}
	if err := steal.Release(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(path); err != nil || target != successorOwner {
		t.Fatalf("successor link = %q, %v", target, err)
	}
}

func TestTaskLockAgedDanglingPrimary(t *testing.T) {
	state, id, path := testLockInputs(t)
	oldOwner := path + ".owner.missing"
	if err := os.Symlink(oldOwner, path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if lock.ownerPath == oldOwner {
		t.Fatal("recovery kept the missing owner")
	}
	if target, err := os.Readlink(path); err != nil || target != lock.ownerPath {
		t.Fatalf("recovery replaced the wrong link: %q, %v", target, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockUnknownOwnerFilePreserved(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy bool
	}{
		{name: "symlink owner"},
		{name: "legacy directory", legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, id, path := testLockInputs(t)
			owner := path
			if !test.legacy {
				owner = path + ".owner.stale"
			}
			if err := os.Mkdir(owner, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(owner, "pid"), []byte("99999999\n"), 0600); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(owner, "leave-alone")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if !test.legacy {
				if err := os.Symlink(owner, path); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := AcquireTaskLock(state, id, 100*time.Millisecond)
			if test.legacy {
				if !errors.Is(err, ErrLockTimeout) {
					t.Fatalf("legacy directory with unrelated file = %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := lock.Release(); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "preserve" {
				t.Fatalf("unrelated owner file = %q, %v", data, err)
			}
		})
	}
}

func TestTaskLockReclaimsNestedSteal(t *testing.T) {
	state, id, path := testLockInputs(t)
	createDeadLink(t, path)
	createDeadLink(t, path+".steal")
	createDeadLink(t, path+".steal.steal")
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockReclaimsLiteralTombstonePrefix(t *testing.T) {
	state, id, path := testLockInputs(t)
	owner := path + ".owner.[literal]"
	if err := os.Mkdir(owner, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "pid"), []byte("99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owner, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(owner, owner+".reaped.99999999"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockAbandonedSamePIDTombstoneResumes(t *testing.T) {
	state, id, path := testLockInputs(t)
	owner := createDeadLink(t, path)
	tomb := owner + ".reaped." + strconv.Itoa(os.Getpid())
	if err := os.Rename(owner, tomb); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockActiveSamePathReaperCannotBeStolen(t *testing.T) {
	state, id, path := testLockInputs(t)
	owner := createDeadLink(t, path)
	tomb := owner + ".reaped." + strconv.Itoa(os.Getpid())
	if err := os.Rename(owner, tomb); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)

	releaseReaper, acquired := tryLockReaper(path)
	if !acquired {
		t.Fatal("could not reserve reaper")
	}
	if _, err := AcquireTaskLock(state, id, 100*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("active reaper acquisition = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("active reaper link changed: %v", err)
	}
	if _, err := os.Stat(tomb); err != nil {
		t.Fatalf("active reaper tombstone changed: %v", err)
	}
	otherID, err := ParseTaskID("task-2")
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(state.String(), ".meta-task-2.lock")
	createDeadLink(t, otherPath)
	otherLock, err := AcquireTaskLock(state, otherID, time.Second)
	if err != nil {
		t.Fatalf("unrelated recovery: %v", err)
	}
	if err := otherLock.Release(); err != nil {
		t.Fatal(err)
	}
	releaseReaper()

	lock, err := AcquireTaskLock(state, id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLockConcurrentInterruptedRecovery(t *testing.T) {
	state, id, path := testLockInputs(t)
	createDeadLink(t, path)
	createDeadLink(t, path+".steal")
	createDeadLink(t, path+".steal.steal")
	const workers = 8
	var group sync.WaitGroup
	results := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			lock, err := AcquireTaskLock(state, id, 3*time.Second)
			if err != nil {
				results <- err
				return
			}
			time.Sleep(time.Millisecond)
			results <- lock.Release()
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}
