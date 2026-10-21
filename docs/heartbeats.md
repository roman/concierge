# Shared supervisor events

The root host schedules supervisor updates for Codex and Claude. It publishes
one durable Tincan event before native delivery. A native notification directs
the owning primary to claim the event, inspect fresh state, post an operator
update, and record an output receipt. Queue acceptance and hook exit do not
complete an event.

## Activation and pause

The scheduler starts disabled. An absent `supervisor.json` requires intentional
activation. State migration, host restart, and native registration preserve this
default. Existing heartbeat configuration retains its interval and quiet limit.

```sh
concierge heartbeat show
concierge heartbeat set --interval 5m --quiet-limit 5
concierge heartbeat enable
concierge heartbeat pause --reason "Operator maintenance"
concierge heartbeat resume
```

`enable` clears only the initial activation pause. An earlier explicit pause
survives activation. An already enabled scheduler requires `resume` to clear a
pause. The pause survives owner replacement and host restart. Automatic quiet
suppression cannot clear it.

The host checks every five seconds. The default reporting interval is five
minutes. Missed intervals coalesce into one pending event. A periodic event
requires a change from the last acknowledged snapshot. That snapshot includes
open actions, pending worker replies, and worker status. Both inbox and periodic
output receipts establish the snapshot. An unchanged snapshot stays suppressed
even with `--quiet-limit 0`. A changed snapshot becomes eligible at the next
reporting deadline. The quiet limit still controls compatibility callers and
operator reminder commands. Only acknowledged periodic output changes the
quiet count.

New pending messages remain eligible during an operator pause or quiet
suppression. The event kind is `inbox`. Its claim permits processing the new
messages through their existing verification protocols. It does not authorize
a periodic reminder. Old pending messages do not hide a newly arrived ID.
Replies that arrive before a pending event is claimed share that event. The
claim collects fresh state. After acknowledged inbox output, periodic events
wait at least 60 seconds, or the configured interval when longer. New messages
bypass this delay and remain eligible on the next host check.

The host stops creating periodic events when workers, pending replies,
acceptance, landing, cleanup, launch intentions, and operator actions are all
complete. Task recovery maintenance keeps its separate policy.

## Native destinations

The adopted shell can register Codex automatically with
[`concierge.codex.enable`](development-shell.md#automatic-codex-startup). The
normal `codex` command then owns a private explicit endpoint. Its native
SessionStart hook registers the exact native thread without a setup prompt.
This deployment option authorizes initial activation. Existing operator pauses
survive future starts.

For a separately managed endpoint, the root must already have a live native
primary owner. Register its owning app-server Unix socket and native thread UUID:

```sh
concierge heartbeat register codex --socket /absolute/owning.sock --thread NATIVE_UUID
```

Run registration from that primary's native tool context. Its inherited
`CODEX_THREAD_ID` must match. Registration verifies the same-user socket peer,
server process incarnation, thread UUID, and working directory. It never
discovers a default daemon. A new owner or destination creates a new binding
generation. Old notifications lose authority.

Codex delivery uses `thread/queue/add`. Busy, interrupted, permission-blocked,
missing, and unknown states defer delivery. Each event has at most three native
attempts per binding. Ambiguous insertion retains an uncertain attempt. A
positive queue or history match can confirm acceptance. Absence from bounded
reads cannot prove insertion failed. An expired processing claim can authorize
one further attempt. Concierge does not switch to another transport after an
uncertain submission.

Claude uses the [generated native hook settings](development-shell.md). A
synchronous activity hook records busy state before each user prompt. A
synchronous Stop hook records readiness. The asynchronous Stop consumer
registers its exact native session, process incarnation, and expiry. It waits
for published host events. A current offer prints the fixed notification and
exits 2 for native `asyncRewake`. The default wait lasts 7h59m inside the
eight-hour native hook limit.
An offer consumes readiness until a later synchronous Stop records readiness
again. A delayed overlapping consumer cannot reuse the earlier ready state.

A consumer renewal preserves the binding generation and retry budget. A
missing, exited, or expired consumer is unavailable. A recent offer or active
processing claim remains valid after the offering process exits normally.
Concierge does not create a replacement hook through a generic wake operation.
Secondary Claude sessions leave the primary activity unchanged. Overlapping
Stop consumers exit quietly.

`heartbeat show` reports activation, operator pause, binding generation,
consumer expiry, pending event, delivery phase, attempts, and output receipt.
`native_status` checks the current owner and destination. A verified binding can
still have unavailable delivery if its native history/readiness API fails.
Inspect `last_error` and the host log. Pending events survive failures.

## Claim and output receipt

Use the exact claim command from the native notification. Preserve its event ID,
binding generation, and `--runtime host` guard:

```sh
concierge heartbeat claim EVENT --generation GENERATION --runtime host
concierge heartbeat ack EVENT --generation GENERATION --runtime host --token CLAIM_TOKEN --output OUTPUT_REFERENCE
```

The command uses `concierge` on PATH when available. The adopted shell supplies
`CONCIERGE_HOME` and `TINCAN_HOME`. Notifications retain explicit roots and the
recipient when their generating process has different or missing settings.
They retain the absolute executable when `concierge` is absent from PATH.
The runtime guard checks the caller against the running host's executable,
revision, roots, and recipient before changing event state. An older binary
rejects the new flag. Restore the adopted shell if verification fails. Do not
remove the guard. A generation from another root still fails event verification.

Act only on `status: current`. A claim lasts two minutes and returns fresh
worker states, pending reply metadata, completion work, and open actions.
Handle worker assignments and results through their existing bound protocols.
End the operator update with the current **Waiting on you** list. Use the
harness notification tool when the claim requests an operator notification.

Post the operator update before `ack`. The output reference identifies that
already posted update. Concierge records a primary-reported receipt; it does
not independently attest a harness message. Recheck owner, destination,
generation, event, and pause at claim and acknowledgement. A stale or expired
claim retains the event. A crash after output can cause duplicate output.
Acknowledging before output could lose the update.

An exact receipt retry succeeds. `status: acknowledged` means the primary must
not repeat the output. The host acknowledges its keyed Tincan publication only
after the durable output receipt. `supervisor.json` and its private advisory
lock recover the event, binding, pause, claim, and receipt after host restart.
Back up that state with the Concierge root.

After activation or an explicit operator pause, `heartbeat check` and
`operator remind` report the host-owned scheduler. They do not run separate
clocks. Their earlier checkpoint behavior remains only for inactive legacy
callers. Generated supervisor instructions use the shared event protocol.

## Native acceptance and limits

Use [Codex startup acceptance](codex-startup-acceptance.md) for the production
module's deployment contract and real-model output gate. The automated tests
below establish transport behavior. Record the production startup and output
results separately.

Opt-in native tests create private roots and new processes. They never discover
or restart an existing daemon. The model responses come from local deterministic
HTTP fixtures. Native approval checks remain active.

* Codex 0.162.1: exact Unix peer and UUID registration passes. Default history
  passes idle queue dispatch, client-ID history reconciliation, and native TUI
  capture of the queued input and response. A fresh
  peer-verified connection also finds dispatched input after the submitting
  connection never reads its queue receipt. The native queue must be empty
  before that history check. Ten fresh default-history trials pass.
* Native turn admission can precede execution and history materialization.
  During startup, both paginated and full-history reads can return RPC -32601,
  `list_turns is not supported yet`. That result does not establish permanent
  incompatibility. The acceptance driver waits for provider execution before
  checking completion. Subsequent readiness errors still fail the test.
  Production read errors retain the pending event and defer delivery until a
  later host check. These results require no history migration or Codex upgrade.
* A separate `historyMode: legacy` trial also passes dispatch, reconciliation,
  and TUI capture. Concierge does not change an operator thread's history mode.
* Claude Code 2.1.296: a private native streaming session runs the synchronous
  busy and ready hooks. Its asyncRewake Stop consumer produces a second visible
  stream response for a supervisor event published by the leased host. The
  unacknowledged event remains pending. This trial uses the supplied generated
  hook bodies and a temporary built Concierge executable. A stream response
  does not establish output in an interactive terminal.

The production Codex startup path passes the full real-model claim, original
TUI output, and durable acknowledgement gate. Two fresh primaries register
automatically and reuse the same host. The operator handles initial folder
trust through the normal native UI. The native workspace sandbox and
`Approve for me` controls remain active. See the
[recorded acceptance evidence](codex-startup-acceptance.md#recorded-native-acceptance).

Claude's full output gate remains incomplete. Its deterministic fixture cannot
provide the native permission classifier. A Bash claim reports
`Classifier unavailable` and retains the event without a claim or receipt.
The trials do not grant trust, answer approval requests, change permission
mode, or manufacture classifier approvals. Claude real-model compliance and
original interactive UI output require their own acceptance gate.

Live deployment remains separate from private acceptance. Observe actual
operator output before acknowledging its reference. Transport success alone
does not establish deployment readiness.

Run the optional trials with explicit installed binary paths:

```sh
CONCIERGE_NATIVE_CODEX=/path/to/codex nix develop -c go test -run '^TestNativeCodexQueueDispatch$' -v ./pkg/codexqueue
CONCIERGE_NATIVE_CODEX=/path/to/codex CONCIERGE_NATIVE_UI_LOOP=/path/to/ui-loop nix develop -c go test -run '^TestNativeCodexQueueDispatch$' -v ./pkg/codexqueue
CONCIERGE_NATIVE_CLAUDE=/path/to/claude CONCIERGE_NATIVE_CONCIERGE=/path/to/built/concierge nix develop -c go test -run '^TestNativeClaudeStopConsumer$' -v ./pkg/conciergecli
```

Normal CI uses protocol fixtures and skips native trials without explicit paths.
Native acceptance confirms transport and lifecycle behavior. The deterministic
responses do not claim model compliance with the claim/post/ack instructions.
