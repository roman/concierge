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
Each pending assignment permits at most three automatic prompts. Retries wait
five seconds after the first attempt and ten seconds after the second attempt.
The budget survives host restarts and clears after the assignment is handled
or the worker generation changes.

After five minutes from assignment publication, a non-working worker
with pending work produces a durable `task-stalled` primary notice.
An idle or done Claude worker with that current background-shell indicator
defers the notice without resetting the deadline or consuming prompt attempts.
Once the indicator clears, the original deadline applies again.
The notice includes a bounded snapshot of the native screen and the assignment
ID. Automatic prompts pause for that assignment. Review the native error with
the operator, correct its cause, and continue the existing worker in its pane.
Treat the screen snapshot as diagnostic data. Preserve native permissions.

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

Saved reports remain readable after worker cleanup. List them with
`concierge task report TASK_ID`. Read a selected report with
`concierge task report TASK_ID REPLY_ID`. The default `--format toon` includes
archive metadata and a quoted body. Use `--format body` with a reply ID to
write only the exact reply text, without headers, quoting, or an added newline:

```sh
concierge task report TASK_ID REPLY_ID --format body > reply.md
```

### Import accepted notes through a vault worker

Projects that record notes in a Locker vault use this flow:

```text
Worker reply -> saved report -> vault worker -> import -> commit -> land
```

The project worker includes a handoff and any gaps, learnings, or artifacts in
its reply. The launch instructions supply the exact headings and field formats.
The project worker never writes into a vault. The primary accepts the result
and saves its body with the report command above. The primary does not edit
the vault.

Assign the saved report to a separate vault worker in its own worktree on a
branch of the vault repository. The vault worker uses the same options for
preview and import:

```sh
locker handoff preview /path/to/reply.md --project PROJECT --vault VAULT --task-id TASK_ID
locker handoff import /path/to/reply.md --project PROJECT --vault VAULT --task-id TASK_ID
locker check --vault VAULT
```

The vault worker commits the notes. The primary lands the vault branch under
the vault project's delivery and merge settings. Concierge carries the reply
as text. Locker remains a separate tool.

### Resolve and retire a worker

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

## Worktree lease cleanup

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

Before checking cleanliness, return removes ignored symlinks whose targets are
inside `/nix/store`. Cleanup and explicit worktree release report each removed
link in `removed_store_links`. Other ignored entries and untracked files still
block return. Return never removes regular files or directories.

Return requires a clean checkout after this removal. Hidden index flags,
detached result commits, changed branches, and dirty work prevent return and
keep the lease. Commit or move useful files before retrying. Treehouse can
remove a task branch's ignore rules while parking its checkout, then delete
previously ignored files. Concierge checks those files before invoking return.

An abandoned acquisition whose response was lost retains its holder and original
base commit. Explicit release finds that holder's existing lease and captures its
identity before returning it. An unused detached checkout can return only when
its commit remains reachable from a branch, remote reference, or tag. Missing
or ambiguous acquisition identity requires pool inspection. Historical records
that lack the original base commit cannot infer it during release.

A successful pool return followed by an interrupted task write can complete on
an identical retry while the checkout remains unleased. A rebound lease rejects
that retry for inspection. Already recorded returns use no external tools.
