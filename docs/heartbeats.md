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
minutes. Missed intervals coalesce into one pending event. The default quiet
limit suppresses further unchanged periodic events after five unchanged output
receipts. `--quiet-limit 0` disables that suppression. A changed snapshot becomes
eligible again. Only acknowledged periodic output changes the quiet count.

New pending messages remain eligible during an operator pause or quiet
suppression. The event kind is `inbox`. Its claim permits processing the new
messages through their existing verification protocols. It does not authorize
a periodic reminder. Old pending messages do not hide a newly arrived ID.

The host stops creating periodic events when workers, pending replies,
acceptance, landing, cleanup, launch intentions, and operator actions are all
complete. Task recovery maintenance keeps its separate policy.

## Native destinations

The root must already have a live native primary owner. Codex also requires its
explicit owning app-server Unix socket and exact native thread UUID:

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

Use the exact claim command from the native notification. Preserve its explicit
roots, recipient, event ID, and binding generation:

```sh
concierge heartbeat claim EVENT --generation GENERATION
concierge heartbeat ack EVENT --generation GENERATION --token CLAIM_TOKEN --output OUTPUT_REFERENCE
```

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

Opt-in native tests create private roots and new processes. They never discover
or restart an existing daemon. The model responses come from local deterministic
HTTP fixtures. Native approval checks remain active.

* Codex 0.162.1: exact Unix peer and UUID registration passes. A separate
  `historyMode: legacy` thread passes idle queue dispatch, client-ID history
  reconciliation, and TUI capture of the queued input and second response.
* Codex 0.162.1 default history store: after a completed turn, both paginated
  history and full-history hydration return RPC -32601,
  `list_turns is not supported yet`. The default-store acceptance test fails.
  Delivery remains unavailable because the adapter cannot verify readiness.
  The legacy trial does not establish default-store compatibility. Concierge
  does not change an operator thread's history mode.
* Claude Code 2.1.296: a private native streaming session runs the synchronous
  busy and ready hooks. Its asyncRewake Stop consumer produces a second visible
  response for a published supervisor event. The unacknowledged event remains
  pending. This trial uses the supplied generated hook bodies and a temporary
  built Concierge executable.

Run the optional trials with explicit installed binary paths:

```sh
CONCIERGE_NATIVE_CODEX=/path/to/codex nix develop -c go test -run '^TestNativeCodexQueueDispatch$' -v ./pkg/codexqueue
CONCIERGE_NATIVE_CODEX=/path/to/codex CONCIERGE_NATIVE_UI_LOOP=/path/to/ui-loop nix develop -c go test -run '^TestNativeCodexLegacyQueueDispatch$' -v ./pkg/codexqueue
CONCIERGE_NATIVE_CLAUDE=/path/to/claude CONCIERGE_NATIVE_CONCIERGE=/path/to/built/concierge nix develop -c go test -run '^TestNativeClaudeStopConsumer$' -v ./pkg/conciergecli
```

The first trial intentionally exposes incompatible default stores. Normal CI
uses protocol fixtures and skips native trials without their explicit paths.
Native acceptance confirms transport and lifecycle behavior. The deterministic
responses do not claim model compliance with the claim/post/ack instructions.
