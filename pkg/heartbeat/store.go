// Package heartbeat stores pending supervisor events until output is reported.
package heartbeat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/roman/concierge/internal/lockfile"
	"github.com/roman/concierge/pkg/stateversion"
)

const ClaimLease = 2 * time.Minute
const maxStateBytes = 2 << 20

type Store struct{ Root string }

type Event struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	Snapshot string    `json:"snapshot"`
	Replies  []string  `json:"replies,omitempty"`
	Claim    *Claim    `json:"claim,omitempty"`
}

type Claim struct {
	Token string    `json:"token"`
	Until time.Time `json:"until"`
}

type Receipt struct {
	EventID string    `json:"event_id"`
	Token   string    `json:"token"`
	Output  string    `json:"output"`
	At      time.Time `json:"at"`
}

type State struct {
	Version      int       `json:"version"`
	Enabled      bool      `json:"enabled"`
	Paused       bool      `json:"paused"`
	PauseReason  string    `json:"pause_reason"`
	Revision     uint64    `json:"revision"`
	Next         time.Time `json:"next"`
	Pending      *Event    `json:"pending,omitempty"`
	Receipt      *Receipt  `json:"receipt,omitempty"`
	LastSnapshot string    `json:"last_snapshot"`
	QuietCount   int       `json:"quiet_count"`
	SeenReplies  []string  `json:"seen_replies,omitempty"`
}

func (store Store) Read(ctx context.Context) (State, error) {
	var result State
	err := store.update(ctx, func(state *State) error { result = *state; return nil })
	return result, err
}

func (store Store) Enable(ctx context.Context, now time.Time) error {
	return store.update(ctx, func(state *State) error {
		if state.Enabled {
			return errors.New("scheduler already enabled; use explicit resume to clear a pause")
		}
		state.Enabled, state.Paused, state.PauseReason = true, false, ""
		state.Revision++
		state.Next = now.UTC()
		return nil
	})
}

func (store Store) Pause(ctx context.Context, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("pause requires a reason")
	}
	return store.update(ctx, func(state *State) error {
		state.Paused, state.PauseReason = true, reason
		state.Revision++
		if state.Pending != nil {
			state.Pending.Claim = nil
		}
		return nil
	})
}

func (store Store) Resume(ctx context.Context) error {
	return store.update(ctx, func(state *State) error {
		if !state.Enabled {
			return errors.New("scheduler is disabled; enable it explicitly")
		}
		state.Paused, state.PauseReason, state.QuietCount = false, "", 0
		state.Revision++
		return nil
	})
}

func available(state *State) error {
	if !state.Enabled || state.Paused {
		return errors.New("supervisor scheduler is disabled or paused")
	}
	return nil
}

func (store Store) Stage(
	ctx context.Context,
	now time.Time,
	snapshot string,
	replies []string,
) (Event, error) {
	var result Event
	err := store.update(ctx, func(state *State) error {
		if err := available(state); err != nil {
			return err
		}
		if state.Pending == nil {
			state.Pending = &Event{
				ID:       rand.Text(),
				Created:  now.UTC(),
				Snapshot: snapshot,
				Replies:  replies,
			}
		}
		result = *state.Pending
		return nil
	})
	return result, err
}

func pending(state *State, id string) (*Event, error) {
	if err := available(state); err != nil {
		return nil, err
	}
	if state.Pending == nil || id == "" || state.Pending.ID != id {
		return nil, errors.New("event is stale or acknowledged")
	}
	return state.Pending, nil
}

func (store Store) Claim(ctx context.Context, id string, now time.Time) (Claim, error) {
	var result Claim
	err := store.update(ctx, func(state *State) error {
		event, err := pending(state, id)
		if err != nil {
			return err
		}
		if event.Claim != nil && now.Before(event.Claim.Until) {
			return errors.New("event processing is in progress")
		}
		result = Claim{Token: rand.Text(), Until: now.Add(ClaimLease).UTC()}
		event.Claim = &result
		return nil
	})
	return result, err
}

func (store Store) Acknowledge(ctx context.Context, id, token, output string, now time.Time) error {
	if strings.TrimSpace(output) == "" {
		return errors.New("post the operator update first; output evidence is required")
	}
	return store.update(ctx, func(state *State) error {
		if state.Receipt != nil && state.Receipt.EventID == id && state.Receipt.Token == token &&
			state.Receipt.Output == output {
			return nil
		}
		event, err := pending(state, id)
		if err != nil {
			return err
		}
		if event.Claim == nil || token == "" || event.Claim.Token != token ||
			!now.Before(event.Claim.Until) {
			return errors.New("claim is missing or expired")
		}
		if state.LastSnapshot == event.Snapshot {
			state.QuietCount++
		} else {
			state.QuietCount = 0
		}
		state.LastSnapshot = event.Snapshot
		state.SeenReplies = append([]string(nil), event.Replies...)
		state.Receipt = &Receipt{EventID: id, Token: token, Output: output, At: now.UTC()}
		state.Pending = nil
		return nil
	})
}

func (store Store) update(ctx context.Context, change func(*State) error) error {
	guard, err := stateversion.AcquireCurrent(ctx, store.Root)
	if err != nil {
		return err
	}
	defer guard.Close()
	root := guard.StateRoot()
	if info, err := root.Lstat(".supervisor.lock"); err == nil && !privateRegular(info) {
		return errors.New("supervisor lock must be a private regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock, err := lockfile.Open(root, ".supervisor.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := samePrivateFile(root, ".supervisor.lock", lock); err != nil {
		return err
	}
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := samePrivateFile(root, ".supervisor.lock", lock); err != nil {
		return err
	}
	data, err := readState(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	state := State{Version: 1, Paused: true, PauseReason: "activation required"}
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state.Version != 1 {
			return errors.New("unsupported supervisor state version")
		}
	}
	if err := change(&state); err != nil {
		return err
	}
	updated, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(updated) > maxStateBytes {
		return errors.New("supervisor state exceeds 2 MiB")
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	return writeState(root, updated)
}

func privateRegular(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func samePrivateFile(root *os.Root, name string, file *os.File) error {
	selected, err := root.Lstat(name)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !privateRegular(selected) || !privateRegular(opened) || !os.SameFile(selected, opened) {
		return errors.New("supervisor file must remain a private regular file")
	}
	return nil
}

func readState(root *os.Root) ([]byte, error) {
	file, err := root.OpenFile("supervisor.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := samePrivateFile(root, "supervisor.json", file); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxStateBytes {
		return nil, errors.New("supervisor state exceeds 2 MiB")
	}
	return data, samePrivateFile(root, "supervisor.json", file)
}

func writeState(root *os.Root, data []byte) error {
	name := ".supervisor-" + rand.Text()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return err
	}
	if err := root.Rename(name, "supervisor.json"); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
