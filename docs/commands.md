# Command reference

| Command | Responsibility |
| --- | --- |
| `tincan inbox` | Send, list, wait for, read, and acknowledge messages. |
| `tincan notify` | Prompt a Herdr endpoint while its inbox has pending messages. |
| `concierge state` | Initialize fresh task state, back up legacy state, migrate, and recover. |
| `concierge backlog` | Queue work, manage holds, dispatch, accept results, and retire worker bindings. |
| `concierge task` | Adopt workers, exchange bound assignments and replies, stop panes, and inspect tasks. |
| `concierge worker` | Inspect recorded worker identities and recovery markers. |
| `concierge primary` | Claim, verify, and release root ownership from native lifecycle hooks. |
| `concierge operator ask ID TEXT` / `resolve ID` / `list` | Record requests for operator input and resolve answered requests. |
| `concierge operator remind [--interval 15m]` | Report due operator requests and unfinished worker retirement. |
| `concierge control` | Inspect, start, and interrupt live Herdr agents. |
| `concierge supervise` | Monitor a manual pane or supervise a task with inbox delivery and worker recovery. |
| `concierge brief` | Show pending primary messages, queued work, workers, and unfinished completions. |
| `concierge project show REPO` | Read operator-owned merge authority and delivery mode for a repository. |
| `concierge project set REPO [--merge on\|off] [--delivery pr\|fast-forward]` | Atomically update approved repository settings. |
| `concierge brief-include set --file FILE` / `clear` | Replace or clear approved standing worker instructions. |
| `concierge land TASK_ID [--operator-approved]` | Land an accepted result with the configured delivery mode. |

## Operator requests and reminders

Before asking for approval, an operator command, or a decision, the primary
records an action with `concierge operator ask ID TEXT`. Use a stable ID for
retries. An identical retry succeeds without restarting its reminder interval.
Different text under the same open ID fails. Resolve the request after the
operator answers with `concierge operator resolve ID`. Resolving an already
resolved action succeeds. `operator list`, `brief`, and `task pending` show
open requests. Accepted worker completions stay in the pending report until
worker retirement and completion finish. Acceptance does not establish that
the worker is currently idle; inspect its pane and agent status before stopping it.

`operator remind` returns `status: due` with the requests to repeat, or
`status: quiet`. `open_items` counts open requests and pending completions.
Reminder times persist across command invocations. Each item has a 15-minute
interval by default. `--interval` accepts a positive duration. A new request
can cause a reminder even during another request's interval. A worker keeps
its reminder interval through all completion phases. Finishing the completion
removes it from future reminders.

The Claude Stop hook checks reminders every 30 seconds during its inbox wait.
The owning Codex primary runs `operator remind` after each inbox checkpoint.
The supervisor repeats open requests in a short **Waiting on you** list at
the end of every reply. A reminder without an operator answer also calls for
a desktop notification through the harness tool when available. Claude uses
`PushNotification`. Notifications do not resolve requests.

## Supervisor delegation and settings

The devshell module installs the supervisor contract through its SessionStart
hook and its linked `concierge-local-supervisor` skill. The primary delegates
project work to workers. Scouts produce reports. A specific operator-approved
operation grants authority only for that operation. The contract belongs to
Concierge; adopting repositories need no AGENTS.md or CLAUDE.md changes.

These optional files belong to the operator. The primary runs settings commands
only after the operator approves the exact change. The commands create `config/`
on demand and replace files atomically. Project updates preserve other repositories
and unspecified fields. Invalid existing configuration stops an update.

| Setting | Path under `$CONCIERGE_HOME` | Default |
| --- | --- | --- |
| Per-repository `merge` | `config/projects.json` | `off`: operator approval for every landing. |
| Per-repository `delivery` | `config/projects.json` | `pr`: worker opens a PR. |
| Standing worker instructions | `config/brief-include.md` | Absent: append no text. |

For example:

```sh
concierge project set my-project --merge on --delivery fast-forward
concierge brief-include set --file ./worker-defaults.md
concierge brief-include clear
```

The project settings file can also contain:

```json
{
  "my-project": { "merge": "on", "delivery": "fast-forward" },
  "another-project": { "merge": "off", "delivery": "pr" }
}
```

Keys match the repository name passed to `backlog add --repo`. Read effective
settings with `concierge project show my-project`. An absent repository or
omitted field uses its default. Invalid values and unknown fields stop the
read. The primary must ask the operator to correct invalid settings.

Merge authority and delivery mode are independent. `merge: on` authorizes the
primary to land green, in-scope work. `merge: off` requires approval for each
landing. `delivery: fast-forward` leaves the worker's clean ready branch for a
fast-forward of the default branch. The worker commits its changes and does not
push or open a PR. `delivery: pr` has the worker push and open a PR.
Neither mode authorizes red work, force pushes, or discarding unlanded work.
Destructive, irreversible, or security-sensitive landings require separate
operator approval even with merge on.
Accept the worker result, then run `concierge land TASK_ID` before worker cleanup.
With merge off, pass `--operator-approved` only after the operator approves that
exact landing. The command checks the recorded lease, branch, and clean worktree,
including ignored files. It discovers the default branch from `origin` and
requires the task branch to contain its fetched tip.

Fast-forward delivery pushes the verified commit to the remote default branch.
It deletes a matching remote task branch when present. An explicit lease makes
that deletion conditional on the verified task commit. A concurrent update keeps
the branch. Branch updates never use force. It advances the main
checkout with `--ff-only` when that checkout is clean and on the default branch.
A dirty, diverged, or differently checked-out main checkout stays unchanged.
The result reports each skipped operation. The local task branch remains available.

PR delivery requires one open, non-draft PR for the recorded branch and exact
commit. It requires a ready merge state and passing required checks, then uses
`gh pr merge --squash --match-head-commit`. It reports success only after a fresh
read confirms the merge and its resulting commit. A queued merge remains
unconfirmed. Neither delivery path force-pushes. Inspect the remote after any
command failure before retrying. Local verification of fast-forward work remains
the worker's responsibility.

`concierge launch` appends the nonempty `config/brief-include.md` verbatim after
a `Standing worker instructions` heading. It preserves the assignment text.
Task-specific instructions take precedence over these standing defaults.
An absent or blank file adds nothing. An unreadable or nonregular file stops
launch before contacting the host. The existing launch intent freezes the
resulting assignment. Keep the include unchanged during an interrupted launch retry; inspect
the intent before changing the request. The include applies to every launched
task, including scouts. Later `task send` messages are task-specific steering.
Standing instructions cannot override the scout's read-only contract.

Queue a scout with `backlog add ID TITLE --kind scout --repo REPO` and launch it
normally. A current `task receive` includes the read-only contract after all
generation and assignment checks. Scouts deliver findings, evidence,
limitations, and open decisions through `task reply send --body-file REPORT`.
Concierge archives every scout reply under `reports/` in the state root using
the durable message format. Reply publication and archival both precede
assignment acknowledgement. An identical retry repairs an interrupted archive.

The primary reads a saved report with `concierge task report ID REPLY_ID`.
The reply ID comes from `task reply send` or the primary inbox. Reports retain
their original generation and survive worker cleanup and removal of the original
mailbox. Each reply remains separate, so a later question or report cannot
overwrite earlier evidence. `status: archived` identifies historical evidence;
use `task reply check` to validate current results before accepting them.
Scout read-only behavior is an agent contract under normal native permissions.
Concierge does not replace those permissions with a filesystem sandbox.

## Independent messages

```sh
tincan inbox send worker-1 --key request-1 --body 'Please check the task.'
tincan inbox list worker-1
tincan inbox read worker-1 MESSAGE_ID
tincan inbox ack worker-1 MESSAGE_ID
tincan inbox wait concierge --timeout 30s
```

A stable key makes an identical send retry return the same message. Reusing the
key with another body returns an error. Use `--body-file FILE` or
`--body-file -` to read a body from a file or stdin. Acknowledge a message after
handling it. A wait returns pending messages or a bounded checkpoint.

```sh
tincan notify --recipient concierge --target PRIMARY_PANE --session work
```

The notifier prompts the selected Herdr endpoint while messages remain pending.
A prompt directs the agent to its inbox. Acknowledgement records that the
recipient handled the message. Notification runs until interruption. Its
interval defaults to five seconds. Use `--herdr PATH` to select another Herdr
executable.
