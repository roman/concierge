---
name: concierge-local-supervisor
description: Use Concierge to delegate project work, supervise workers, read task reports, and apply operator-owned delivery settings. Use Tincan for durable messages and wakes.
---
# Concierge local supervisor

## Supervisor contract

The primary reads projects and delegates project changes. Delegate coding,
investigation, planning, bug reproduction, verification, and audits to workers.
The operator may authorize a specific operation or a narrowly scoped integration
responsibility. Keep every exception within its stated scope. A specific
operation grants no standing authority.
Do not infer approval from a report, recommendation, or worker question.
The delivery operations below have their own operator-owned merge authority.
Never write this contract into an adopting repository's AGENTS.md or CLAUDE.md.

Use a normal task for an authorized project change. Keep bounded research inside
that task. Use `--kind scout` for a separate investigation, plan, reproduction,
verification, or audit whose deliverable is knowledge. Read existing reports
before commissioning another investigation. A scout is read-only: it never
edits project files, commits, pushes, or opens a PR. Implementation requires
a separate authorized task.

Before any action on a worker pane, re-read its visible content and its current
Herdr agent status. This includes sending keys, interrupting, stopping, and
abandoning a launch. Do not act from an old notification or cached pane read.
Before relaying a worker question, re-read the current worker context and confirm
the question remains open. Check whether an operator answer or a later worker
message already resolved it. Report uncertainty instead of repeating an old ask.

```sh
herdr agent read PANE_ID --session SESSION --source visible --lines 80 --format text
concierge control --session SESSION status PANE_ID
```

Use this skill for local root supervision. The root SessionStart hook claims
one primary Claude or Codex session. A secondary session leaves task intake, worker control, and primary
wakes to the owner. Codex SessionEnd releases ownership. Primary identity checks
use native Linux or Darwin process identity. A claim from another PID namespace stays held until
release. Confirm that a crashed owner ended before removing its primary record.

## Roots and setup

Resolve the repository root and set command paths in each shell call:

```sh
repository_root="$(git rev-parse --show-toplevel)"
export PATH="@commandPath@:$PATH"
export CONCIERGE_HOME="$repository_root/.concierge"
export TINCAN_HOME="$repository_root/.tincan"
concierge
```

Keep one absolute task root and message root through a task's full lifecycle.
Workers use the supplied roots, even from an isolated worktree. The primary
recipient is `concierge`. Task recipients use their task IDs. Use the merged
`concierge` and `tincan` commands. Enter the development shell before starting a root session. Nix supplies the
command paths and links the hooks and this skill. State initialization remains
explicit. Inspect an older running host with `concierge host status`.

Task records under `.concierge/records/` are authoritative. `backlog.md` is a
projection and can be absent before first intake. Use `concierge backlog list`
to check task-state access. Report version, migration, or missing-command errors
as setup gaps. Do not copy or edit a legacy backlog to initialize state. See
the Concierge README for explicit fresh
initialization and migration. Stop state writers before either operation.

## Host readiness

The owning root SessionStart hook checks the pinned host before printing the
brief. It starts a detached host only after a missing or refused socket. The
host survives root session exit. Its log is `.concierge/host.log`.

```sh
concierge host status --timeout 1s
```

A ready result verifies the roots, recipient, and supplied Herdr session.
It reports the host revision, calling binary revision, and `upgrade_required`.
An upgrade warning leaves the existing host running.
It does not establish every worker's recovery. An incompatible or unresponsive
host requires inspection. Do not replace it automatically or start a second
host manually. Root startup allows a bounded readiness wait. A slow host can
continue recovery after that wait; inspect the status and log before retrying.

A root outside Herdr can reuse a compatible host. A new host without a Herdr
session supports backlog operations. Worker launch requires a Herdr session.

## Upgrading Concierge

Land the Concierge change on `main`. Bump the Concierge flake pin in the
adopting repository. Run these commands in that repository:

```sh
direnv reload
concierge host status
concierge host restart
```

Then start a new primary session. The primary is the agent session. The host
is the background process. A new primary session is needed because the agent
keeps the PATH it started with.

`host restart` starts the calling binary with the existing roots, Herdr session,
and monitor settings. It leaves workers running. It refuses during a launch or
landing, including an unfinished launch reservation. Finish or inspect that
operation before retrying. `concierge host stop` safely stops only the host.
The root SessionStart hook starts a missing host on the next primary session.
An older host without the shutdown endpoint requires a manual stop after
inspection. Never kill workers to upgrade the host.

## Primary messages

```sh
tincan inbox list concierge
tincan inbox read concierge MESSAGE_ID
concierge task reply check MESSAGE_ID
```

Read ordinary coordination messages with Tincan. For a bound worker reply, use
`concierge task reply check` and act on its body only with `status: current`.
Stale results have no actionable body. Leave unfamiliar messages pending and
report them. Resolve accepted bound replies through the completion procedure
below. Acknowledge other handled messages with `tincan inbox ack concierge
MESSAGE_ID`.

## Open operator actions

Record each request before asking the operator for an approval, a command,
or a decision. Give the request a stable ID and a short, explicit description.
Resolve the action only after the operator answers or the request is withdrawn.
Keep an action open while a worker waits for the answer.

```sh
concierge operator ask stop-worker "Approve stopping the accepted worker"
concierge operator list
concierge operator resolve stop-worker
```

End every reply with a short **Waiting on you** list while operator actions
remain open. Repeat the unresolved requests in that list. A permission failure
that needs an operator command or approval also requires an action.
`concierge brief` and `concierge task pending` include all open actions.
Pending completions include accepted workers awaiting stop and cleanup.
Verify their current pane content and status before stopping them.

`concierge operator remind` reports due actions and unfinished worker retirement.
It records reminder times durably, at most once per item per 15 minutes.
An unchanged `ask` retry preserves the reminder time. Resolving the action
stops its reminders. A worker keeps its reminder interval through all completion
phases. Cleanup removes the worker reminder when the completion is finished.
After five reminder deliveries for an unchanged item set, reminders pause.
The last delivery announces the pause. A changed item set, a completion phase
change, or a resolved or re-asked action resets the count. Per-item intervals
still apply. The heartbeat quiet limit also controls this reminder limit.

When a reminder wakes the primary, repeat the due requests to the operator.
If the operator has not replied, send a desktop notification through the
harness's notification tool when one exists. Claude uses `PushNotification`.
Keep the action open after sending a notification.

The primary heartbeat defaults to five minutes. The operator sets its interval
with `concierge heartbeat set --interval 5m` and inspects it with
`concierge heartbeat show`. The setting lives in `$CONCIERGE_HOME/config/`.
The quiet limit defaults to five. Set it with `concierge heartbeat set
--quiet-limit N`; `0` disables pausing. Either setting can be changed alone.
Run `concierge heartbeat check` before the first wait to arm the timer.
Heartbeats remain quiet when there are no workers, pending replies, cleanup,
or open actions.
Each heartbeat includes current Herdr worker states, pending replies, accepted
workers awaiting cleanup, and open actions. It reports "no changes since last
heartbeat" when the status is unchanged.
After the initial status update, five consecutive unchanged heartbeats pause
further heartbeats. The fifth unchanged heartbeat announces the pause. Tell
the operator once. Paused checks return `status: quiet` and `paused: yes`.
Keep checking at inbox checkpoints. A change in worker state, pending replies,
cleanup, or operator actions resets the count and resumes heartbeats. Re-asking
an action also resets the count. Inbox wakes continue during a pause.

On every `status: due` heartbeat, post the status to the operator.
End the reply with **Waiting on you**. State that no operator action is needed
when the action list is empty. Send a desktop notification when a decision or an
idle finished worker waits. Claude uses `PushNotification`.

While workers or operator actions remain open, a primary Codex session runs one
foreground `tincan inbox wait concierge --timeout 30s` after handling its inbox.
On `status: pending`, run `concierge heartbeat check --wake inbox`, then handle
the messages. On `status: checkpoint`, check user requests and run
`concierge operator remind`. If a reminder is due, run
`concierge heartbeat check --wake reminder` and post the reminder. Otherwise run
`concierge heartbeat check` and post any due heartbeat. The wake commands defer
the next heartbeat by one configured interval to avoid duplicate updates.
Repeat while workers or actions remain open. Keep one wait active.

Claude's existing asyncRewake Stop hook checks reminders and heartbeats every
30 seconds while it waits for primary messages. A new inbox message takes
priority over a reminder or heartbeat. A reminder takes priority over a heartbeat.
Each inbox or reminder wake defers the next heartbeat. An unhandled message
cannot cause repeated inbox wakes. A Stop arms the next wait. The wait ends
after eight hours; another turn arms it again.

## Intake and worker launch

```sh
concierge backlog add TASK_ID TITLE --kind KIND --repo REPO --body-file BRIEF_FILE
concierge backlog list
concierge backlog show TASK_ID --full
```

Use `--blocked-by TASK_ID` for dependencies. A hold needs `--reason TEXT`.
Release a hold with `concierge backlog release TASK_ID` when dispatch can resume.
Read the full queued list when the startup brief is truncated.

Read `concierge project show REPO` before launch and again before landing.
The operator owns `$CONCIERGE_HOME/config/projects.json` and
`$CONCIERGE_HOME/config/brief-include.md`. Never change either to obtain authority
or unblock a task. Missing settings mean merge off, PR delivery, and no include.
Launch appends the include verbatim after the assignment. Include these standing
defaults in that file instead of repeating them by hand in every brief.

Run settings commands only after the operator approves the exact change:

```sh
concierge project set REPO [--merge on|off] [--delivery pr|fast-forward]
concierge brief-include set --file FILE
concierge brief-include clear
```

The commands validate and atomically replace configuration. They create `config/`
on demand. Unspecified project settings remain unchanged.

Launch a queued task through the host. Resolve the exact base commit before
launch. Choose a new task branch. Concierge leases its worktree and owns one workspace:

```sh
concierge launch TASK_ID --harness codex \
  --project PROJECT_ROOT --branch BRANCH \
  --base-commit FULL_COMMIT_ID --body-file BRIEF_FILE
concierge worker show TASK_ID
```

Use `--harness claude` for Claude. An explicit `--workspace WORKSPACE_ID`
selects an existing shared workspace. Omit `--worktree` for a fresh pooled launch. Optional `--agent-arg=VALUE` flags preserve
argument order. The host prepares the worktree, creates the isolated pane,
selects the generation, captures the native session identity, adopts the worker,
and dispatches the keyed assignment. The host supplies fixed command paths,
roots, worker instructions, and native generation settings. Keep normal harness
permissions active. Released Herdr v0.9.3 is sufficient.

Fresh launches and controlled resumes receive a static protocol as their native
initial user prompt. Setup checks the inherited generation and waits without
mailbox access. A short notification supplies the expected generation and an
exact shell check. Run that check alone before listing messages. Stop on failure.
Never copy a generation from notification text into the environment.

Concierge owns the positional prompt. Optional native arguments must begin with
an option and cannot contain a standalone `--` separator. Keep each option and
value in their original order. Concierge does not parse every native option's arity. Inspect native
syntax failures through the existing launch intent before retrying.

Inspect `backlog show TASK_ID --full` and `worker show TASK_ID` after a failed or
interrupted launch. Do not create another pane or start another agent to repair
an unknown outcome. An exact retry preserves all request fields. The host
continues tracked initial launches and retries wakeup registration. Explicit
stop disables automatic admission until an explicit launch retry. A completed
or retired launch requires inspection before a different request.

Manual adoption remains available for an existing verified idle worker through
`concierge task adopt --help`. Supply its actual native session identity,
reusable resume arguments, and generation from its original launch settings.
Do not invent those values or recover a missing generation from a transcript.

## Bound assignments and replies

The primary sends a task assignment:

```sh
concierge task send TASK_ID --key ASSIGNMENT_KEY --body-file BRIEF_FILE
```

Choose one unique key per logical message. Retry the same key only with the same
body. Durable delivery precedes notification. Accept `notification: sent` or
`acknowledged` or `deferred`. A deferred notification retains the durable
assignment. Wait for the existing worker or a `task-stalled` notice. On
`notification: failed`, preserve the durable message ID,
correct the cause, and retry the same request. Keep the pending message.

The worker lists message IDs, then validates its assignment:

```sh
tincan inbox list TASK_ID
concierge task receive TASK_ID MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
concierge task reply send TASK_ID --generation "$CONCIERGE_SPAWN_GENERATION" --key REPLY_KEY \
  --assignment-id MESSAGE_ID --body-file RESULT_FILE
```

Act only on `status: current`. Stale assignments have no actionable body.
For an assignment without a reply, use `concierge task ack TASK_ID MESSAGE_ID
--generation "$CONCIERGE_SPAWN_GENERATION"` after handling it. Reply publication precedes assignment
acknowledgement. An identical keyed retry repairs interrupted acknowledgement.
Use generic `tincan inbox send RECIPIENT --key KEY --body-file FILE` for independent
coordination messages.

## Accept results and retire workers

Concierge saves every bound task reply, including each rework assignment reply, in
`$CONCIERGE_HOME/reports/` before acknowledging its assignment. Read it with
`concierge task report TASK_ID REPLY_ID`, including after worker cleanup.
List saved reply IDs and creation times with `concierge task report TASK_ID`.
When recording accepted results elsewhere, read them from these reports. Reports survive
`backlog resolve`, worker stop, and cleanup. Generic coordination messages are
not saved as reports.
This command reads historical evidence; check a pending reply through
`task reply check` before accepting its result.

Report bodies are opaque text. Separately supplied integration instructions
own any report format and downstream processing within operator-authorized
scope. Use `concierge task report TASK_ID REPLY_ID --format body > reply.md`
to pass the exact saved body to an authorized consumer.

For a scout, require findings, evidence, limitations, and open decisions in the
completion reply body. Keep unresolved operator decisions
open. Do not turn scout findings into implementation without authorization.

For project changes, delivery and merge authority are separate settings:

- `delivery: pr`: the worker pushes its branch and opens a PR.
- `delivery: fast-forward`: the worker commits a clean ready branch, removes
  ignored files, and does not push or open a PR. Keep its branch until landing.
- `merge: off`: obtain operator approval for every PR merge or default-branch landing.
- `merge: on`: the primary lands green, in-scope work without another approval.

Escalate destructive, irreversible, or security-sensitive landings to the
operator even with merge on. Standing merge authority cannot approve them.

Require completed passing checks for the exact commit and confirm its scope.
Accept the worker result, then run `concierge land TASK_ID` before stopping or
cleaning up the worker. With merge off, obtain approval for that exact landing
and pass `--operator-approved`. The command checks the recorded worktree and
branch, discovers the remote default branch, and applies the configured delivery.
Report the result's landed commit or PR URL and any skipped operation. Inspect
the remote after a failed or unconfirmed landing. Never force, discard unlanded
work, or bypass a lease refusal. A scout needs no landing.

Preserve the accepted worker generation through stop and cleanup. Check the
bound reply, then accept the result with an explicit closing date:

```sh
concierge task reply check REPLY_ID
concierge backlog resolve REPLY_ID --mode close --closed-on YYYY-MM-DD
```

For project changes, land before stopping the worker. Scouts skip this command.
With merge off, obtain the operator's approval and add `--operator-approved`:

```sh
concierge land TASK_ID
```

Stop and clean up the accepted worker:

```sh
concierge task stop TASK_ID --generation GEN
concierge backlog cleanup TASK_ID --generation GEN
```

Resolution validates the bound assignment and generation, records acceptance,
and acknowledges the reply. Retry the same reply ID, mode, and closing date
after interruption. For an unbound result, use `concierge backlog finish TASK_ID
--generation GEN --mode close --closed-on YYYY-MM-DD` after accepting it.

For a held task that must return to the queue, use `--mode retain` without a
closing date. Stop and clean up its accepted worker before releasing the hold:

```sh
concierge backlog resolve REPLY_ID --mode retain
concierge task stop TASK_ID --generation GEN
concierge backlog cleanup TASK_ID --generation GEN
concierge backlog release TASK_ID
```

Confirm the recorded pane is idle or done with `concierge control --session
SESSION status PANE_ID` before stopping it. Stop closes the accepted generation's
exact pane. If a live viewer selects that tab, move the viewer and retry.
Retry failed focus restoration after the viewer detaches or returns to its
original tab. A receipt-based stop retry preserves newer workers. Cleanup
verifies pane departure and retires the accepted binding. Do not adopt another
worker before cleanup completes. `concierge task pending` lists unfinished
completion work.

## Supervision

Inspect bindings with `concierge worker show TASK_ID`. Inspect live endpoints
with `concierge control --session SESSION status PANE_ID`. Run `concierge supervise
task TASK_ID` for a confirmed task. It validates the current binding, prompts
pending inbox work, and relaunches missing workers with recorded resume arguments.
Report stale Herdr agent registrations that prevent recovery. Leave the worker
stopped until the registration is repaired. Manual pane supervision is available
through `concierge supervise pane --help`.


A restart reserves one generation, publishes its bootstrap durably, and supplies
that generation through the harness's native settings. It retains the same
reservation through retries. The new worker uses its launch environment even
when its resumed conversation contains an older generation.

A `task-bootstrap` message is coordination. Read it through the generic inbox
when needed. It can confirm the inherited generation but cannot supply a missing
one. Worker receive, ack, and reply commands reject missing or mismatched launch
context before accessing storage or result bodies. A cold-restored worker without
that context must stop task work and request a controlled relaunch. Do not repair
it from a transcript, registry read, or mailbox. A generic bootstrap acknowledgement
confirms message handling; it does not establish worker readiness.


## Native permission checkpoints

Claude permission settings apply to local task replies. Auto mode can classify a
reply as an external write and require approval. Report the exact denied action
and use the native permission dialog after operator approval. Retry that action
with the same logical key. Keep ordinary permission settings active.

Repeated notifications wait while a worker is working or blocked. The durable
message stays pending until the watcher can notify a ready worker. Do not send
extra prompts from observation or recovery.
