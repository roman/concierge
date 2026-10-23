# Task lifecycle


Task mutations require a running host service and a live primary claim from a
Claude or Codex SessionStart hook. Start the host outside the model sandbox:

```sh
concierge --root /absolute/project/.concierge --message-root /absolute/project/.tincan \
  host --session work serve
```

Initialize or migrate the state before starting the host. Stop the host before
an offline migration. Only one host can serve a state root. The host checks the
calling process against the current primary claim. Intake fails without the
service and never falls back to a local write. An ordinary Codex tool sandbox
also blocks the client connection. Run intake and the routed lifecycle commands with explicitly approved
unsandboxed execution, or a socket permission verified to preserve the caller
identity. No saved sandbox configuration changes automatically.
Backlog changes, cleanup, reply resolution, adoption, assignment send, task stop,
and manual control use the same authenticated service. Worker receive, reply,
and acknowledgement commands retain their launch-generation checks.

The host fixes its message root, recipient, Herdr executable, manual session,
and monitor settings at startup. Explicit client settings must match. Omitted
client settings use the host configuration. Task workers use their recorded
sessions. A host without a manual session can supervise tasks; manual control
requires a fixed host session. Use `host --herdr PATH`, `--interval DURATION`,
`--restart-tolerance N`, and `--restart-window DURATION` to configure the host.

Startup attempts to restore eligible in-flight task wakeups before reporting
`status: ready`. Each failed restoration reports a diagnostic and leaves the
host available for repairs. Retry supervision explicitly after fixing the task.
Readiness confirms completed restoration attempts and an available host.

Inspect readiness without a primary claim:

```sh
concierge --root /absolute/project/.concierge --message-root /absolute/project/.tincan \
  host --session work status --timeout 1s
```

The probe verifies the state root, message root, recipient, installed package,
and an explicitly selected session. Without a session expectation, it can reuse
the host's fixed session. Explicit Herdr settings must also match. The response
contains no task or message bodies. Only the host's user can inspect it.

| Exit | Status | Meaning |
| --- | --- | --- |
| 0 | ready | The compatible host accepts requests. Worker recovery can still need attention. |
| 1 | unavailable | The socket is missing or refuses a connection. |
| 2 | incompatible or error | Inspect a package/configuration mismatch, permission denial, unsupported probe, or protocol error. |
| 3 | unresponsive | The bounded request timed out. Startup, overload, or a stuck host can cause this. |

An automatic startup attempt requires an unavailable result. A timeout does not
establish that the host is starting. Do not replace an incompatible or unresponsive
host automatically. The probe does not start, stop, or modify a host.

Queue a task from the primary session and read its full contents:

```sh
concierge backlog add task-1 'Fix startup' --kind task --repo my-project --body-file brief.txt
concierge backlog list
concierge backlog show task-1 --full
```

An identical intake retry can repair a failed backlog projection. A changed
request for the same task ID returns a conflict. Repeat `--blocked-by ID` to
record dependencies. Use `backlog hold task-1 --reason TEXT` and
`backlog release task-1` to control dispatch.

Choose a fresh random generation before starting the idle worker. Supply it as
`CONCIERGE_SPAWN_GENERATION` through the harness launch settings. Adopt its verified
pane with that same generation and the exact Herdr session, name, project,
worktree, and reusable resume arguments:

```sh
concierge task adopt task-1 --generation GEN --name worker-1 --session work --pane PANE_ID \
  --project /absolute/project --worktree /absolute/worktree \
  --agent-arg resume --agent-arg THREAD_ID
concierge worker show task-1
concierge backlog start task-1
```

Adoption verifies the live endpoint and records the supplied generation. It does
not inspect the worker environment or start task dispatch. The start command
checks holds and dependencies. The worker uses its launch generation for its
assignment and reply commands. Worker `receive`, `ack`, and `reply send` require
`CONCIERGE_SPAWN_GENERATION` in their tool environment to match `--generation`.
A missing or mismatched launch value stops the command before storage or body
access. Request a controlled relaunch; never recover the value from a transcript,
task record, or mailbox. This guards cooperating workers, not arbitrary callers.
Keep generation overrides out of the reusable
resume arguments; a controlled restart supplies its own reserved generation.
Reusable Claude arguments must omit `--settings`. Reusable Codex arguments must
omit `shell_environment_policy` overrides. Use normal user or project settings
for other configuration. Conflicting stored options fail before a restart
reservation or adoption binding is written.

```sh
concierge task send task-1 --key assignment-1 --body-file brief.txt
concierge task receive task-1 MESSAGE_ID --generation GEN
concierge task reply send task-1 --generation GEN --key answer-1 \
  --assignment-id MESSAGE_ID --body-file reply.txt
```

`task send` stores a generation-bound assignment before notifying the worker.
A `notification: deferred` result confirms delivery without an immediate prompt.
Working workers, retry backoff, and exhausted prompt budgets defer notification.
Claude workers also defer assignment prompts while the current turn summary
above the input reports running background shells. Claude starts a new turn
when a background shell finishes. Historical summaries and tool output do not
defer prompts. A blocked worker or an unreadable screen retains the normal
stall checks.
The newest unanswered pending assignment in the current generation controls
the notification budget. A fresh follow-up starts a new budget even while an
older assignment awaits completion or has a frozen stall notice. The prompt
asks the worker to read its pending list, which still includes the older work.
Retrying the same message key does not start a new budget. Stale assignments
cannot release a current assignment's stall.

Each notification budget permits at most three automatic prompts. Retries
wait five seconds after the first attempt and ten seconds after the second
attempt. The budget survives host restarts and clears after the selected
assignment is handled or the worker generation changes.
If the selected assignment is handled while older work remains pending,
supervision selects that older work with a new notification budget.

After five minutes from assignment publication, a non-working worker
with pending work produces a durable `task-stalled` primary notice.
An idle or done Claude worker with that current background-shell indicator
defers the notice without resetting the deadline or consuming prompt attempts.
Once the indicator clears, the original deadline applies again.
The notice includes a bounded snapshot of the native screen and the assignment
ID. Automatic prompts pause for that assignment. Review the native error with
the operator, correct its cause, and continue the existing worker in its pane.
Treat the screen snapshot as diagnostic data. Preserve native permissions.

After correcting the cause of a stall, send a fresh follow-up with a new key
through `task send`. Use `concierge supervise task TASK_ID` to restore delivery
if supervision has stopped. An identical supervision start preserves the
current notification budget. A fresh follow-up does not answer a native
approval dialog or replace the worker's generation or binding.

A failed notification leaves the assignment durable. Retry the same keyed
request after correcting the worker or notification error. The worker acts on
an assignment only when `receive` reports `status: current`. Stale assignments
have no actionable body. For work that needs no reply, use
`task ack task-1 MESSAGE_ID --generation GEN` after handling the assignment.

The reply command publishes the result before acknowledging its assignment.
An identical keyed retry completes interrupted acknowledgement. The primary
checks a result without accepting it, then resolves a bound reply:

```sh
tincan inbox list concierge
concierge task reply check REPLY_ID
concierge backlog resolve REPLY_ID --mode close --closed-on YYYY-MM-DD
concierge task stop task-1 --generation GEN
concierge backlog cleanup task-1 --generation GEN
```

### Follow-up work for an accepted scout

Reopen a local scout before sending another question:

```sh
concierge task reopen scout-1
concierge task send scout-1 --key follow-up-1 --body-file question.txt
```

Reopen requires an accepted result and a retained worker binding. A live worker
keeps its generation. If the agent has departed, Concierge resumes the native
session from the recorded resume arguments through normal supervised recovery.
The restart advances the generation and uses the usual bootstrap and recovery
protocol. A missing pane gets a replacement workspace without taking focus.
The host must still have access to the recorded Herdr session and worktree.

Concierge refuses recovery without an explicit recorded native session ID. It
does not search transcripts. A lost pane-creation response is recovered by its
reserved workspace label. If that workspace cannot be found, the command reports
an unresolved creation instead of creating another workspace. Retry `task reopen`
after fixing the reported problem. Repeating a successful reopen is a no-op for
the task history.

Local project tasks can also reopen while their worktree lease is held and their
committed result is absent from the fetched target branch. Use `task reopen TASK_ID`,
then `task send` to request a rebase and fresh project checks. Accept the new result
before retrying `land`. Landed project tasks require a new task. Remote
workers, held tasks, and workers retired by cleanup cannot reopen. An active
stop or cleanup operation blocks reopen and appears by name in the error.

The task record keeps the earlier acceptance, closing date, timestamp, and reopen
reason in its `reopens` history. `backlog show` displays the most recent reopen.
Earlier reports remain available. Reopen supersedes the earlier acceptance for
the active work. A follow-up reply is saved as a report and needs a new
`backlog resolve` decision. Replaying the earlier resolution cannot accept active
follow-up work.

Saved reports remain readable after worker cleanup. List them with
`concierge task report TASK_ID`. Read a selected report with
`concierge task report TASK_ID REPLY_ID`. The default `--format toon` includes
archive metadata and a quoted body. Use `--format body` with a reply ID to
write only the exact reply text, without headers, quoting, or an added newline:

```sh
concierge task report TASK_ID REPLY_ID --format body > reply.md
```

For a short decision preview, use the same format on either command:

```sh
concierge task reply check REPLY_ID --format concise
concierge task report TASK_ID REPLY_ID --format concise
```

The preview includes the status, task, generation, reply ID, UTF-8 body size,
the first paragraph of `Handoff` → `Outcome`, and the titles under `Gaps`,
`Learnings`, and `Artifacts`. A standalone `## Outcome` also works. Open
decision and question sections include the first line of each paragraph or
list item. Nested decision headings include their first line and list items.
Recognized headings are `Open decisions`, `Open questions`, `Waiting on you`,
`Operator decisions`, and `Decision required` (case insensitive).

This is a lossy Markdown preview. It recognizes ATX headings (`## Title`)
outside fenced code blocks. Outcome previews stop at 600 characters, section
titles at 120, and decision lines at 240. An ellipsis marks a shortened field.
An empty outcome means no supported outcome section was found. Read the full
body before deciding when the preview lacks the required evidence.

Both commands retain their full-body default for existing consumers. Explicit
`--format toon` includes the complete quoted body. Report `--format body`
remains exact. A stale reply check returns only its status and task in either
format. A saved report always reports `status: archived`; it does not validate
the current worker generation.

The supervisor skill uses `--format concise` for initial reply inspection and
saved-report navigation. Before acceptance, it requires a fresh full check
(`--format toon`) when decision evidence is missing, including an empty outcome.
Only current reply bodies are actionable. Exact imports into authorized
downstream consumers use report `--format body`, redirected without printing the
body again for supervision. Saved-report previews do not replace generation or
acceptance checks.

### External report consumers

Worker replies can include named files with repeated `--attach NAME=PATH`
arguments. Saved reports retain their bytes after worker cleanup. Report
`--format json` includes the validated attachment bytes as base64. Report
`--attachment NAME --output NEW_FILE` exports one file without overwriting an
existing path. See [reply attachments](reply-attachments.md) for limits, remote
recovery, and the stable interface for external importers.

Concierge saves reply bodies as opaque text. An integration can define a
report format in the task assignment and consume the exact saved body through
`task report --format body`. The integration owns its schemas, downstream
processing, and storage policies.

Check a pending reply through `task reply check` before accepting it. Saved
reports provide historical evidence and do not grant authority to act. Assign
downstream project changes to an authorized task under the normal delivery and
merge settings. Separately supplied role-specific instructions may define
additional responsibilities within explicit operator authority.

### Resolve and retire a worker

Close a held task after its worker has retired or its launch has been abandoned:

```sh
concierge backlog close TASK_ID --generation GENERATION \
  --reason "Superseded by a replacement task" --closed-on YYYY-MM-DD
```

For held retired work, the generation must identify its latest retained result
or an abandonment receipt. The command records a closure decision separately from result acceptance.
It preserves the hold, receipts, worktree, commits, and lease reservation. An
existing pane, completion, or launch intent blocks closure. An identical retry
succeeds; a different reason, generation, or date fails. Inspect the reason and
date with `backlog show TASK_ID`. Use `backlog list --state held` to find held
queued tasks. Return a retained lease separately with `backlog release-worktree`
after reviewing its work.

For an in-flight confirmed local worker with a held pool lease, the same command
requires its exact generation, verified pane absence, and no pending current bound
reply. It archives the binding and retains the worktree, branch, and lease;
pending restarts still use `backlog recover`.

Act on a reply only when `check` reports `status: current`. Resolution verifies
its assignment and worker generation, records acceptance, and acknowledges the
reply. Close requires an explicit valid closing date. Retry the same reply ID,
mode, and date after interruption. For a held task that must return to the
queue, use `--mode retain` without a closing date. Finish the retained worker's
cleanup before releasing its hold:

```sh
concierge backlog resolve REPLY_ID --mode retain
concierge task stop task-1 --generation GEN
concierge backlog cleanup task-1 --generation GEN
concierge backlog release task-1
```

For a result without a bound reply, use `backlog finish task-1 --generation GEN
--mode close --closed-on YYYY-MM-DD`. The equivalent Retain request uses
`--mode retain` without a date, followed by stop, cleanup, and release.

For an active accepted generation, stop closes its exact recorded pane. A retry
for a retired generation can succeed from its receipt and leaves a newer worker
unchanged. Cleanup verifies pane departure and retires the accepted worker
binding. Preserve the accepted generation through these
steps. Stop and cleanup join the matching wakeup before their pane operations.
Their task and endpoint reservations block conflicting operations until they
finish. Invalid cleanup leaves supervision running. An operation failure after
wakeup shutdown reports that supervision is stopped. Inspect the worker before
restarting its wakeup. `task pending` lists unfinished completion work. `task cancel` releases
an unbound adoption reservation; it rejects confirmed bindings.

### Discard a missing worker

Use `task discard` only with explicit operator authority for the recorded
generation:

```sh
concierge task discard task-1 --generation GEN --operator-approved
```

The primary host joins the exact task controller and checks fresh structured
pane absence, agent absence, and workspace inventories. Unknown or live
resources cause refusal. The command currently supports confirmed local pooled
workers after launch completion. Remote workers, unfinished launches,
replacement generations, accepted results, and saved or pending reports require
their existing recovery or review paths.

The command inventories tracked changes, untracked files, ignored files and
commits beyond the recorded launch base. Recoverable work produces a
`decision required` error with the worktree, branch, base, HEAD, files and
commits. The binding, lease and source product remain available for the
operator's decision. Commits beyond the base require review even if another
branch already contains them. Discard inventory never removes ignored files.

A successful discard queues the unfinished task and removes the missing worker
binding. It records `kind: discarded_worker` in the existing resource
abandonment journal. It creates no accepted completion or result receipt.
The exact lease return uses the existing pool ownership, process and cleanliness
guards. The branch remains available. A refusal remains an error.

The discard record is durable before pool return. If return fails, the record
keeps its held lease. Repeat the same command after resolving the refusal.
Retries use the saved generation and never stop a newer worker. Inspect the
task after any failure because supervision can already be stopped.

Both the CLI and the primary host must run a build that includes this command.
An older host rejects the new operation. Activate the new host through the
normal controlled host replacement procedure. Keep the existing task root,
message root, and operator settings. Discard does not change heartbeat or
reminder settings.

## Worktree lease cleanup

Landing permits ignored development-shell outputs in a worker checkout. Changed
tracked files and untracked files still block landing. Landing preserves ignored
files and skips advancing the main checkout if a committed file would overwrite
an ignored local file. Worktree lease return retains its stricter cleanup checks.

Stale replies remain in the primary inbox until explicitly retired. Checking a
reply never acknowledges it. To remove an obsolete reply from heartbeat's
`pending_replies`, use the same roots and recipient as the primary:

```sh
concierge task reply check MESSAGE_ID
concierge task reply retire-stale MESSAGE_ID
```

Retirement checks the binding again under the task lock. It refuses current
replies, including replies preserved across a worker restart. It moves only the
selected stale message to handled. It changes no task state and keeps the report
archive. Identical retries succeed. Messages with missing or corrupt task
records, invalid envelopes, or unrelated keys remain for inspection. This
command runs locally and does not require a new host protocol operation.

Accept the result, stop the worker, and retire the accepted generation:

```sh
concierge task stop task-1 --generation GEN
concierge backlog cleanup task-1 --generation GEN
```

Cleanup requires confirmation that the exact worker pane departed. For a closed
result, it returns the lease and records `lease: returned`. A retained result
keeps its lease and records `lease: held`. Unaccepted results keep their leases.
Removing a task hold does not release its worktree.

Explicitly release a retained result or an abandoned acquisition with:

```sh
concierge backlog release-worktree task-1 --generation GEN
```

The command uses the primary host admission and the recorded generation. It
preserves a newer worker and its supervision. The lease ID and holder must still
match. The pool must report no live processes. Return uses neither --force nor
approval to discard changes. The result branch remains in the repository.

A live-process refusal is separate from a changed lease or damaged checkout.
It lists up to 16 blocking PIDs from Treehouse, with the available owner, parent
PID, and command name. `omitted_processes` counts additional blockers. The
refusal covers all blockers, including omitted processes. Missing details
appear as `unavailable`. On Linux,
`pane_hint` reports the process's inherited `HERDR_PANE_ID`, when readable.
The hint does not establish ownership. Retired receipts do not retain a worker
pane, so `pane_relation` remains `unknown`.

Diagnostics query only the listed PIDs and limit the process snapshot to 64 KiB
and two seconds. Owner, command, and pane-hint fields contain at most 128 bytes
plus a truncation marker. Command arguments and other environment values are
excluded. Linux pane-hint collection reads at most 64 KiB of each process's
environment. Darwin reports the pane hint as `unavailable`.

Process details come from a later snapshot. A process can exit, become
inaccessible, or have its PID reused between snapshots. Inspect the process
before taking action. Close an operator-owned terminal only after confirming
its purpose, then retry cleanup. Concierge preserves the lease regardless of
the process's owner or pane hint. It never kills a blocking process.

After recorded acceptance in close mode and confirmed landing, return runs
`git reset --hard HEAD` and `git clean -fdx` in the checkout. An accepted scout
needs no landing. Lease identity and process checks precede deletion. The same
policy applies to local leases, native remote leases, and imported primary
result leases. Return verifies the checkout is clean before releasing it.

Cleanup and explicit release report removals in `removed_paths` and the host
log. A directory path covers all removed contents beneath it. The path report
is bounded to 24 KiB per checkout. An oversized report stops cleanup before
deletion and requires manual cleanup. Partial failures report completed removals.

Unaccepted, abandoned, and unlanded work never receives this destructive clean.
Explicit release of an abandoned or retained lease keeps the conservative
policy: ignored Nix store symlinks, empty ignored directories, and files in the
operator's `return_ignored_paths` list may be removed. Other ignored files,
untracked files, and tracked changes block return and retain the lease.

All workers, including scouts, must remove generated ignored outputs created by
their tools before reporting. Preserve pre-existing ignored files and report
cleanup blockers. Inspect `git status --short --ignored` to include ignored
directories in the check.

The diagnostic `task supervision is stopped` describes the wakeup controller
after a lifecycle operation. It does not prevent another cleanup attempt.
Resolve the reported checkout or process condition and repeat the same cleanup
command with the same generation. A host restart is unnecessary for a retry.

Hidden index flags, detached result commits, changed branches, and active
processes prevent return and keep the lease. A project result must appear on
the freshly fetched target branch, or match a merged PR whose merge commit is
on that branch. An unavailable landing check retains the checkout for retry.
Commit or move useful files before acceptance and landing.

An abandoned acquisition whose response was lost retains its holder and original
base commit. Explicit release finds that holder's existing lease and captures its
identity before returning it. An unused detached checkout can return only when
its commit remains reachable from a branch, remote reference, or tag. Missing
or ambiguous acquisition identity requires pool inspection. Historical records
that lack the original base commit cannot infer it during release.

A successful pool return followed by an interrupted task write can complete on
an identical retry while the checkout remains unleased. A rebound lease rejects
that retry for inspection. Already recorded returns use no external tools.
