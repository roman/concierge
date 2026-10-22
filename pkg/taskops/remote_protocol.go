package taskops

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/taskmeta"
)

// RemoteWorkerTools contains paths on the selected machine.
type RemoteWorkerTools = remoteworker.Tools

// RemoteWorkerProtocol contains no primary filesystem paths or credentials.
func RemoteWorkerProtocol(taskID string, tools RemoteWorkerTools) (string, error) {
	if _, err := taskmeta.ParseTaskID(taskID); err != nil {
		return "", err
	}
	if err := tools.Validate(); err != nil {
		return "", err
	}
	command, task := notificationShellQuote(tools.Concierge), notificationShellQuote(taskID)
	resultFile := notificationShellQuote(filepath.Join(tools.TaskDirectory, "result.md"))
	protocol := fmt.Sprintf(
		`You are a remote Concierge worker for %[1]s. Keep existing user instructions and native permissions active.
At setup, run a standalone shell check that CONCIERGE_SPAWN_GENERATION is inherited and nonempty. Confirm setup. Await a short supervisor notification. Do not access the mailbox during setup.
On each notification, first compare the inherited generation with the expected generation in a standalone shell call. Stop and report a missing or different inherited generation. Never set or export a generation copied from a notification, transcript, registry or message.
Read a fresh pending list:
%[2]s task inbox list %[1]s
Use only MESSAGE_ID values from that list. Receive each assignment through:
%[2]s task receive %[1]s MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
Act on its body only when status is current. Never repeat acknowledged work. The remote client uses the separate worker socket. Keep its credential file private. Do not read or copy the secret into commands, prompts, logs or Git.
If a native sandbox restriction blocks a socket connection, request permission through the normal native approval workflow. Keep the sandbox and approval rules active. Stop and report a rejected native approval. A native socket restriction does not establish that Concierge access is revoked.
Read settings for the assigned project with:
%[2]s task settings %[1]s --generation "$CONCIERGE_SPAWN_GENERATION"
For project changes, finish with a committed, clean, ready branch. Workers do not push or open a PR. The primary fetches the branch. It handles landing or PR delivery under the project settings. Preserve the worktree and branch until the primary confirms cleanup. Scouts follow their read-only contract.
All workers, including scouts, must remove generated ignored files created by their tools before reporting. Inspect git status --short --ignored first. Preserve pre-existing ignored files and report any cleanup problem. Scouts may remove their own generated ignored outputs without changing tracked files.
Write the result to %[3]s. Send it with a stable REPLY_KEY:
%[2]s task reply send %[1]s --generation "$CONCIERGE_SPAWN_GENERATION" --key REPLY_KEY --assignment-id MESSAGE_ID --body-file %[3]s
Retain the result until publication, archive and assignment acknowledgement are confirmed. Retry only the identical original reply. Stop retries after access is revoked. Ask the primary to reconcile it. Never resubmit under a new generation.
For a completed assignment that needs no reply:
%[2]s task ack %[1]s MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
Handle task-bootstrap messages separately. Inspect a bootstrap with:
%[2]s task bootstrap read %[1]s MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
The primary verifies the bootstrap's state root, task and generation. Check kind task-bootstrap. Check your task and inherited generation. A bootstrap carries no new work. Acknowledge a matching bootstrap with:
%[2]s task bootstrap ack %[1]s MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
Report other message kinds or mismatched bootstrap context. Keep unprocessed messages pending.
`,
		task,
		command,
		resultFile,
	)
	return strings.ReplaceAll(protocol, "\n", " "), nil
}
