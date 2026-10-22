package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/concierge/pkg/taskview"
	"github.com/roman/concierge/pkg/versioncheck"
	"github.com/roman/tincan/pkg/inbox"
)

func Collect(
	ctx context.Context,
	root, messageRoot, recipientName, executable string,
) (Observation, error) {
	store, err := taskstate.NewStore(root)
	if err != nil {
		return Observation{}, err
	}
	tasks, err := store.List(ctx)
	if err != nil {
		return Observation{}, err
	}
	ledger, err := readActions(filepath.Join(root, "operator-actions.json"))
	if err != nil {
		return Observation{}, err
	}
	recipient, err := inbox.ParseRecipient(recipientName)
	if err != nil {
		return Observation{}, err
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return Observation{}, err
	}
	pending, err := messages.List(recipient)
	if err != nil {
		return Observation{}, err
	}
	replies := 0
	var replyRows strings.Builder
	workerReplies := map[string]bool{}
	for _, message := range pending {
		if strings.HasPrefix(message.Key, "task-reply:"+root+":") {
			replies++
			fmt.Fprintf(&replyRows, "pending_reply: %s\n", message.ID.String())
			identity := strings.TrimPrefix(message.Key, "task-reply:"+root+":")
			if separator := strings.LastIndex(identity, ":"); separator >= 0 {
				workerReplies[identity[:separator]] = true
			}
		}
	}
	var workers strings.Builder
	bound, completions := 0, 0
	launches := 0
	notify := len(ledger.Actions) > 0
	now := time.Now()
	for _, task := range tasks {
		snapshot := task.Snapshot()
		if snapshot.LaunchIntent != nil && snapshot.Worker == nil {
			launches++
			if task.UnregisteredLaunchOverdue(now) {
				notify = true
			}
		}
		if snapshot.Completion != nil {
			completions++
			fmt.Fprintf(
				&workers,
				"cleanup: %s, %s, %s\n",
				strconv.Quote(task.ID()),
				snapshot.Completion.Phase,
				strconv.Quote(snapshot.Completion.Generation),
			)
		}
		worker := snapshot.Worker
		if worker == nil {
			continue
		}
		bound++
		client, err := herdr.NewClient(executable).ForEndpoint(worker.Machine, worker.Session)
		if err != nil {
			return Observation{}, err
		}
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		agent, err := client.Get(statusCtx, worker.Pane)
		cancel()
		state := agent.Status
		if err != nil {
			state = herdr.Unknown
		}
		fmt.Fprintf(
			&workers,
			"worker: %s, %s, %s\n",
			strconv.Quote(task.ID()),
			state,
			strconv.Quote(worker.Generation),
		)
		finished := snapshot.Completion != nil || workerReplies[task.ID()+":"+worker.Generation]
		if state == herdr.Done || (state == herdr.Idle && finished) {
			notify = true
		}
	}
	var view strings.Builder
	for _, message := range pending {
		if notice, ok := versioncheck.PendingNotification(message, root); ok {
			fmt.Fprintf(
				&view,
				"version_warning_task: %s\nversion_warning_message: %s\n",
				strconv.Quote(notice.Task),
				strconv.Quote(message.ID.String()),
			)
			if err := notice.Report.Write(&view); err != nil {
				return Observation{}, err
			}
			notify = true
		}
	}
	fmt.Fprintf(
		&view,
		"workers: %d bound\npending_replies: %d\nawaiting_cleanup: %d\n",
		bound,
		replies,
		completions,
	)
	view.WriteString(workers.String())
	if launches > 0 {
		fmt.Fprintf(&view, "unbound_launches: %d\n", launches)
	}
	if err := taskview.WriteStuckLaunches(&view, tasks, now); err != nil {
		return Observation{}, err
	}
	view.WriteString(replyRows.String())
	writeActions(&view, ledger.Actions)
	fmt.Fprintf(&view, "operator_revision: %d\n", ledger.ActionRevision)
	return Observation{
		Replies: pendingIDs(pending, root),
		Active: bound > 0 || launches > 0 || len(ledger.Actions) > 0 ||
			len(pendingIDs(pending, root)) > 0 ||
			completions > 0,
		Text:   view.String(),
		Notify: notify,
	}, nil
}

type actions struct {
	Actions        map[string]string `json:"actions"`
	ActionRevision uint64            `json:"action_revision"`
}

func readActions(path string) (actions, error) {
	result := actions{Actions: map[string]string{}}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("operator actions must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > maxStateBytes {
		return result, errors.New("operator actions exceed 2 MiB")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if result.Actions == nil {
		return result, errors.New("invalid operator actions")
	}
	for id, text := range result.Actions {
		if _, err := taskmeta.ParseTaskID(id); err != nil || strings.TrimSpace(text) == "" {
			return result, errors.New("invalid operator action")
		}
	}
	return result, nil
}

func writeActions(output *strings.Builder, items map[string]string) {
	fmt.Fprintf(output, "operator_actions: %d open\n", len(items))
	if len(items) == 0 {
		return
	}
	keys := make([]string, 0, len(items))
	for id := range items {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	fmt.Fprintf(output, "operator_actions[%d]{id,text}:\n", len(keys))
	for _, id := range keys {
		fmt.Fprintf(output, "  %s,%s\n", strconv.Quote(id), strconv.Quote(items[id]))
	}
}

func pendingIDs(messages []inbox.Message, root string) []string {
	var ids []string
	for _, message := range messages {
		if !strings.HasPrefix(message.Key, "supervisor-event:"+root+":") {
			ids = append(ids, message.ID.String())
		}
	}
	sort.Strings(ids)
	return ids
}
