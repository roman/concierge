package heartbeat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/roman/tincan/pkg/inbox"
)

type EventKind string

const (
	Periodic EventKind = "periodic"
	Inbox    EventKind = "inbox"
)

type Settings struct {
	Interval   string `json:"interval"`
	QuietLimit int    `json:"quiet_limit"`
}

func (settings Settings) interval() (time.Duration, error) {
	interval, err := time.ParseDuration(settings.Interval)
	if err != nil || interval <= 0 || settings.QuietLimit < 0 {
		return 0, errors.New("heartbeat interval must be positive and quiet limit nonnegative")
	}
	return interval, nil
}

func ReadSettings(path string) (Settings, error) {
	settings := Settings{Interval: "5m", QuietLimit: 5}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return settings, err
	}
	if !info.Mode().IsRegular() {
		return settings, errors.New("heartbeat settings must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return settings, err
	}
	if len(data) > 65536 {
		return settings, errors.New("heartbeat settings exceed 64 KiB")
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	_, err = settings.interval()
	return settings, err
}

type Observation struct {
	Active  bool
	Text    string
	Notify  bool
	Replies []string
}

func unseen(replies, seen []string) bool {
	for _, reply := range replies {
		if !slices.Contains(seen, reply) {
			return true
		}
	}
	return false
}

// A digest keeps receipts from doubling the persisted size of large snapshots.
func snapshotHash(snapshot string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot)))
}

func (store Store) Schedule(
	ctx context.Context,
	now time.Time,
	observation Observation,
	settings Settings,
) (State, error) {
	interval, err := settings.interval()
	if err != nil {
		return State{}, err
	}
	var result State
	err = store.update(ctx, func(state *State) error {
		defer func() { result = *state }()
		if !state.Enabled {
			return nil
		}
		newReplies := unseen(observation.Replies, state.SeenReplies)
		if state.Pending != nil {
			if newReplies {
				promoteInbox(state.Pending, now)
			}
			return nil
		}
		if !observation.Active {
			state.Next = time.Time{}
			return nil
		}
		if !newReplies && (state.Paused || now.Before(state.Next)) {
			return nil
		}
		state.Next = now.Add(interval).UTC()
		if !newReplies && state.Receipt != nil && state.Receipt.SnapshotHash != "" &&
			snapshotHash(observation.Text) == state.Receipt.SnapshotHash {
			return nil
		}
		kind := Periodic
		if newReplies {
			kind = Inbox
		}
		state.Pending = &Event{
			Notify:   observation.Notify,
			ID:       rand.Text(),
			Kind:     kind,
			Created:  now.UTC(),
			Snapshot: observation.Text,
			Replies:  append([]string(nil), observation.Replies...),
			Interval: interval,
		}
		state.Delivery, state.LastError = "pending_publication", ""
		return nil
	})
	return result, err
}

// Publish retains an identical keyed message through transport retries. Only a
// durable output receipt permits removal from the primary inbox.
func (store Store) Publish(
	ctx context.Context,
	messageRoot string,
	recipient inbox.Recipient,
) error {
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return err
	}
	var publicationError error
	err = store.update(ctx, func(state *State) error {
		if state.Receipt != nil && state.Receipt.MessageID != "" {
			id, err := inbox.ParseMessageID(state.Receipt.MessageID)
			if err != nil {
				return err
			}
			if err := messages.Acknowledge(recipient, id); err != nil {
				publicationError = err
				state.LastError = err.Error()
				return nil
			}
			state.Receipt.MessageID, state.LastError = "", ""
		}
		event := state.Pending
		if event == nil || event.MessageID != "" {
			return nil
		}
		body := fmt.Sprintf(
			"Concierge supervisor event %s for root %q. Fetch fresh state through concierge heartbeat claim using the native notification's generation. This mailbox record does not acknowledge processing or operator output.",
			event.ID,
			store.Root,
		)
		message, err := messages.Send(recipient, "supervisor-event:"+store.Root+":"+event.ID, body)
		if err != nil {
			publicationError = err
			state.Delivery = "publication_failed"
			state.LastError = err.Error()
			return nil
		}
		event.MessageID = message.ID.String()
		state.Delivery, state.LastError = "published", ""
		return nil
	})
	return errors.Join(err, publicationError)
}

func promoteInbox(event *Event, now time.Time) {
	if event.Claim != nil && now.Before(event.Claim.Until) {
		return
	}
	event.Kind, event.Claim = Inbox, nil
}
