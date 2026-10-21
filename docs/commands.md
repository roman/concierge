# Command reference

| Command | Responsibility |
| --- | --- |
| `concierge --version` | Report the package version and source revision. |
| `concierge host status` | Report the host and binary revisions and flag an upgrade. |
| `concierge host restart [--timeout 15s]` | Safely replace the background host with the calling binary. |
| `concierge host stop [--timeout 15s]` | Safely stop the host and leave workers running. |
| `tincan inbox` | Send, list, wait for, read, and acknowledge messages. |
| `tincan notify` | Prompt a Herdr endpoint while its inbox has pending messages. |
| `concierge state` | Initialize fresh task state, back up legacy state, migrate, and recover. |
| `concierge backlog` | Queue work, manage holds, dispatch, accept results, and retire worker bindings. |
| `concierge backlog close TASK_ID --generation GENERATION --reason TEXT --closed-on YYYY-MM-DD` | Close held retired work or a confirmed absent local worker with no pending current reply. Preserve receipts and worktree reservations. |
| `concierge backlog recover TASK_ID --generation GENERATION --reason TEXT --closed-on YYYY-MM-DD --operator-approved` | Close an approved pending local restart after confirming resource absence. Preserve its worktree and lease. |
| `concierge task` | Adopt workers, exchange bound assignments and replies, stop panes, and inspect tasks. |
| `concierge worker` | Inspect recorded worker identities and recovery markers. |
| `concierge primary` | Claim, verify, and release root ownership from native lifecycle hooks. |
| `concierge operator ask ID TEXT` / `resolve ID` / `list` | Record requests for operator input and resolve answered requests. |
| `concierge operator remind [--interval 15m]` | Report due operator requests and unfinished worker retirement. |
| `concierge heartbeat show` / `set` / `enable` / `pause` / `resume` | Configure and inspect the durable host scheduler. |
| `concierge heartbeat register codex` / `claim` / `ack` | Bind native delivery, claim fresh state, and record posted output. |
| `concierge control` | Inspect, start, and interrupt live Herdr agents. |
| `concierge supervise` | Monitor a manual pane or supervise a task with inbox delivery and worker recovery. |
| `concierge brief` | Show pending primary messages, queued work, workers, and unfinished completions. |
| `concierge project show REPO` | Read operator-owned merge authority and delivery mode for a repository. |
| `concierge project set REPO [--merge on\|off] [--delivery pr\|fast-forward]` | Atomically update approved repository settings. |
| `concierge brief-include set --file FILE` / `clear` | Replace or clear approved standing worker instructions. |
| `concierge land TASK_ID [--operator-approved]` | Land an accepted result with the configured delivery mode. |
| `concierge land-recovered TASK_ID --generation GENERATION --commit COMMIT --operator-approved` | Land an explicitly approved local result after the task closes and its lease returns. |

## Host upgrades

The host is the background process. The primary is the agent session.
`host status` returns `host_revision`, `binary_revision`, and `upgrade_required`.
A ready host can report an upgrade requirement while continuing to serve work.
An older host without revision metadata reports `unknown`.

After landing a Concierge change, bump its flake pin in the adopting repository.
Run `direnv reload`, then `concierge host restart`. Start a new primary session
to refresh the agent's inherited PATH. Restart preserves the state root, message
root, recipient, Herdr session, and monitor settings. Workers remain running.
An active landing, launch, or unfinished launch reservation prevents restart.
The replacement uses the calling binary's runtime tools. Its log remains
`host.log` in the state root. A timeout requires inspection before another start.

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

The shared host scheduler includes open requests and unfinished worker
retirement in supervisor events. The primary repeats unresolved requests in
its **Waiting on you** list and uses the harness notification tool when a
claim requests a notification. Notifications do not resolve requests.

After scheduler activation or an explicit operator pause, `operator remind`
returns `status: quiet` and `scheduler: host_owned`. It does not maintain a
second reminder clock. Its older 15-minute per-item behavior remains for
inactive legacy callers. See the [shared scheduler](heartbeats.md) for the
operator pause, quiet limit, native delivery, and output receipt protocol.

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
| Per-repository `delivery` | `config/projects.json` | `pr`: local worker or remote primary opens a PR. |
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
push or open a PR. `delivery: pr` has the local worker push and open a PR.
For a remote worker, the primary imports its accepted commit and publishes the PR.
Neither mode authorizes red work, force pushes, or discarding unlanded work.
Destructive, irreversible, or security-sensitive landings require separate
operator approval even with merge on.
Accept the worker result, then run `concierge land TASK_ID` before worker cleanup.
With merge off, pass `--operator-approved` only after the operator approves that
exact landing. The command checks the recorded lease, branch, and clean worktree,
including ignored files. It discovers the default branch from `origin` and
requires the task branch to contain its fetched tip.

For a closed task whose local worktree lease has already returned, use
`concierge land-recovered TASK_ID --generation GENERATION --commit COMMIT
--operator-approved`. The operator must approve that exact task, generation, and
full reviewed commit, even when `merge: on`. The command supports
`delivery: fast-forward` only. It requires the latest close receipt and the
recorded returned local lease. It reserves the recorded free Treehouse slot
without changing its checkout. It releases the reservation after landing.
The lease holder must identify this state root and task using
`concierge:ROOT:TASK_ID:ACQUISITION_GENERATION`. A restart keeps its original
acquisition generation. The latest close receipt must match `--generation`.
It recovers its own prior reservation after an interrupted attempt. The
command requires the clean worktree to use the recorded repository and
branch. It requires worktree HEAD to equal `--commit`, the recorded base to
be an ancestor of that commit, and the fetched default branch tip to be an
ancestor too. A missing, occupied, damaged, or reused slot stops landing.
Use `--treehouse PATH` to select a Treehouse binary with the `lease` command.
Keep the worktree and
task receipt intact until the landing finishes.

Fast-forward delivery pushes the verified commit to the remote default branch.
It deletes a matching remote task branch when present. An explicit lease makes
that deletion conditional on the verified task commit. A concurrent update keeps
the branch. Branch updates never use force. It advances the main
checkout with `--ff-only` when it has no tracked changes and is on the default
branch. Ignored and untracked local files do not block this check. Git refuses
the fast-forward if an untracked file would be overwritten. Worker worktrees
must still be clean, including ignored and untracked files.

A skipped checkout advance creates an open operator action with the checkout
path, the reason, and a `git -C PATH pull --ff-only origin BRANCH` command.
Later skips update the same action. A later landing that advances the checkout
resolves the action and removes its reminder. Other operator actions remain
unchanged. A ledger write failure reports that the commit already landed.
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
Concierge archives every bound task reply under `reports/` in the state root using
the durable message format. Reply publication and archival both precede
assignment acknowledgement. An identical retry repairs an interrupted archive.
Rework assignment replies receive separate reports. Generic coordination
messages are not archived. Scouts also retain archival for unbound replies.

The primary reads a saved report with `concierge task report ID REPLY_ID`.
List saved reply IDs and archive creation times with `concierge task report ID`.
The list uses creation order and returns an explicit zero count for an empty
archive. The reply ID also comes from `task reply send` or the primary inbox.
Reports retain their original generation and survive worker cleanup and removal of the original
mailbox. Each reply remains separate, so a later question or report cannot
overwrite earlier evidence. `status: archived` identifies historical evidence;
use `task reply check` to validate current results before accepting them.
Both commands accept `--format concise` for a short outcome, section-title,
and open-decision preview with the reply ID and body size. Full-body output
remains the default; `--format toon` requests it explicitly. Use report
`--format body` for exact text. See [task reports](tasks.md) for preview limits
and supervisor adoption requirements.
When recording accepted results elsewhere, the primary reads them from these reports,
including after `backlog resolve`, worker stop, and cleanup.
Worker replies accept repeated `--attach NAME=PATH` arguments. Report
`--format json` includes their validated bytes. Report
`--attachment NAME --output NEW_FILE` exports one named file to a new path.
See [reply attachments](reply-attachments.md) for limits and the import interface.
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

## Primary heartbeat

`concierge heartbeat show` reports the interval, quiet limit, activation,
operator pause, native binding, pending delivery, attempts, and output receipt.
`heartbeat set --interval 2m --quiet-limit 5` changes the operator-owned
configuration. Either flag preserves the other setting. `0` disables automatic
quiet suppression. An explicit operator pause still requires `resume`.

The scheduler starts disabled. `heartbeat enable` activates it intentionally.
`heartbeat pause --reason REASON` persists an operator pause. `heartbeat resume`
clears the pause only after activation. Registration and host restart preserve
those settings.

`heartbeat register codex --socket PATH --thread UUID` binds the exact native
primary destination. Claude's generated activity and Stop hooks register its
native pull consumer. `heartbeat claim EVENT --generation GEN` fetches fresh
state and grants a two-minute processing claim. After posting the operator
update, use `heartbeat ack EVENT --generation GEN --token TOKEN --output REF`.

`heartbeat activity` and `heartbeat wait [--timeout 7h59m]` consume native Claude
hook JSON on stdin. They require the owning native process, matching project,
and verified hook settings. They do not schedule independent periodic events.
After activation or an explicit pause, the compatibility `heartbeat check`
command reports the host-owned scheduler.

The [shared supervisor guide](heartbeats.md) documents migration, native
compatibility limits, claim fencing, and acceptance tests.
