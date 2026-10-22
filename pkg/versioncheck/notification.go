package versioncheck

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/tincan/pkg/inbox"
)

type Notification struct {
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Task   string `json:"task"`
	Report Report `json:"report"`
}

func Publish(
	messageRoot string,
	recipient inbox.Recipient,
	root, taskID string,
	report Report,
) error {
	if len(report.Warnings) == 0 {
		return nil
	}
	if _, err := taskmeta.ParseTaskID(taskID); err != nil {
		return err
	}
	notice := Notification{
		Kind:   "task-version-warning",
		State:  filepath.Join(root, "tasks"),
		Task:   taskID,
		Report: report,
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	messages, err := inbox.New(messageRoot)
	if err != nil {
		return err
	}
	_, err = messages.Send(recipient, notificationKey(root, taskID, body), string(body))
	return err
}

func notificationKey(root, taskID string, body []byte) string {
	return fmt.Sprintf("task-version-warning:%s:%s:%x", root, taskID, sha256.Sum256(body))
}

// PendingNotification accepts only this state's informational warning messages.
// Its result grants no authority to execute an assignment or accept a worker.
func PendingNotification(message inbox.Message, root string) (Notification, bool) {
	if !strings.HasPrefix(message.Key, "task-version-warning:"+root+":") {
		return Notification{}, false
	}
	var notice Notification
	if json.Unmarshal([]byte(message.Body), &notice) != nil ||
		notice.Kind != "task-version-warning" ||
		notice.State != filepath.Join(root, "tasks") ||
		len(notice.Report.Warnings) == 0 {
		return Notification{}, false
	}
	if _, err := taskmeta.ParseTaskID(notice.Task); err != nil ||
		message.Key != notificationKey(root, notice.Task, []byte(message.Body)) {
		return Notification{}, false
	}
	return notice, true
}
