---
name: concierge-local-supervisor
description: Use Concierge for local task intake and worker supervision at the Exmachina root. Use Tincan for durable messages and wakes.
---
# Concierge local supervisor

Use this skill for local root supervision. Secondmates and Relay are outside
this workflow. The root SessionStart hook claims one primary Claude or Codex
session. A secondary session leaves task intake, worker control, and primary
wakes to the owner. Codex SessionEnd releases ownership. Primary identity checks
use native Linux or Darwin process identity. A claim from another PID namespace stays held until
release. Confirm that a crashed owner ended before removing its primary record.

## Roots and setup

Resolve the Exmachina root and set command paths in each shell call:

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
explicit. An older running host requires inspection and a manual restart.

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

A ready result verifies the package, roots, recipient, and supplied Herdr session.
It does not establish every worker's recovery. An incompatible or unresponsive
host requires inspection. Do not replace it automatically or start a second
host manually. Root startup allows a bounded readiness wait. A slow host can
continue recovery after that wait; inspect the status and log before retrying.

A root outside Herdr can reuse a compatible host. A new host without a Herdr
session supports backlog operations. Worker launch requires a Herdr session.

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

While tasks remain active, a primary Codex session runs one foreground
`tincan inbox wait concierge --timeout 30s` after handling its current inbox.
Handle messages on `status: pending`. Check user requests on `status: checkpoint`.
Repeat only while tasks remain active. Keep one wait active. Claude's background
Stop hook waits for primary messages and wakes the owner. Handle its inbox after
each wake. A Stop arms the next wait. The wait ends after eight hours; another
turn arms it again.

## Intake and worker launch

```sh
concierge backlog add TASK_ID TITLE --kind KIND --repo REPO --body-file BRIEF_FILE
concierge backlog list
concierge backlog show TASK_ID --full
```

Use `--blocked-by TASK_ID` for dependencies. A hold needs `--reason TEXT`.
Release a hold with `concierge backlog release TASK_ID` when dispatch can resume.
Read the full queued list when the startup brief is truncated.

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
continues tracked initial launches and retries resident registration. Explicit
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

Preserve the accepted worker generation through stop and cleanup. Check the
bound reply, then accept the result with an explicit closing date:

```sh
concierge task reply check REPLY_ID
concierge backlog resolve REPLY_ID --mode close --closed-on YYYY-MM-DD
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
