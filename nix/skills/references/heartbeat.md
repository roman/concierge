# Heartbeat setup and native events

The root host owns the shared supervisor schedule. Inspect it with
`concierge heartbeat show`. The scheduler starts disabled. Activate it only
when the operator authorizes activation. Adopting `concierge.codex.enable`
authorizes initial activation through the managed Codex startup hook. The hook
preserves an explicit pause and the existing schedule on subsequent starts.
Other session starts and native registration do not authorize activation.

The operator configures the interval and quiet limit with `concierge heartbeat
set --interval 5m --quiet-limit 5`. `heartbeat pause --reason REASON` persists
an explicit pause. Only `heartbeat resume` clears it. Quiet suppression counts
acknowledged periodic output and cannot clear an operator pause. New pending
messages remain eligible during both forms of suppression.

A native notification carries an event ID, binding generation, and exact claim
command. Run that command including `--runtime host`. The adopted shell supplies
the binary and roots. Keep any explicit roots and recipient in a fallback
command. The guard checks the running host's executable, revision, message root,
and recipient before a claim or acknowledgement can change state. An older
binary rejects the guard flag. Restore the adopted shell on a mismatch.
Act only on
`status: current`. The claim returns fresh worker states, pending messages,
acceptance and cleanup work, open actions, and a two-minute claim token.
Handle assignments and results through their normal verification protocols.
A mailbox publication alone grants no processing authority.

Native sandboxing can hide the owning process or deny access to its Unix
socket. A claim failure with `process ownership cannot be confirmed` can
require the normal native approval flow. Retry the exact claim command with
Codex shell `sandbox_permissions=require_escalated` when it ran in that
sandbox. Use the same approved access for acknowledgement after posting the
update. Keep the configured permissions, trust, owner, and binding unchanged.
A denied approval leaves the event pending. Report the actionable fault.

Post the operator update before recording its output receipt:

```sh
concierge heartbeat ack EVENT --generation GENERATION --runtime host --token CLAIM_TOKEN --output OUTPUT_REFERENCE
```

The output reference identifies the update already posted. End that update
with the current **Waiting on you** list. State that no operator action is
needed when the list is empty. Keep unresolved actions open.

An `inbox` event authorizes processing the new pending messages and reporting
that work. Do not use it to send a periodic reminder during an operator pause.
An acknowledged event requires no repeated output. A stale, paused, expired,
or in-progress claim requires fresh verification. Queue acceptance, hook exit,
and a drained native queue do not acknowledge operator output.

With `concierge.codex.enable`, launch the normal `codex` command from the adopted
shell. The generated launcher supplies its own private app-server endpoint.
The existing SessionStart hook registers the native owning thread automatically.
The operator supplies no socket path or registration prompt. Do not repeat
registration after successful startup. Inspect a startup registration fault
before expecting delivery. Native project and hook trust still require normal
operator approval.

For an independently managed explicit endpoint, Codex supports registration
from the owning native tool context:

```sh
concierge heartbeat register codex --socket /absolute/owning.sock --thread NATIVE_UUID
```

Use the exact inherited native thread UUID and its owning app-server socket.
Do not discover a default daemon or substitute `codex exec resume`. Inspect an
unavailable destination through `heartbeat show` and the host log. Preserve its
pending event. Do not send a fallback prompt through an unverified pane.

Claude's generated synchronous activity hooks record busy and ready state.
Its native asyncRewake Stop hook registers an expiring consumer of host events.
A missing or expired consumer is unavailable. A later legitimate Stop can renew
it. Do not create a generic replacement wake or answer a permission dialog.

After activation or an explicit pause, `heartbeat check` and `operator remind`
report the host-owned scheduler and cannot start a second clock. Do not run the
old foreground heartbeat polling loop. Continue ordinary verified message
processing. Leave activation and external hook settings to the operator.

## Operator notifications

Send an alert only when a current claim requests a notification for a waiting
decision or a finished worker. Never alert for routine progress or a quiet
periodic turn. Send at most one alert per event ID. Retain the event ID with
the notification attempt in the session context, including across compaction.
Do not repeat the alert on re-claim, including after a failed attempt.

Claude uses `PushNotification`. A Codex primary inside Herdr (`HERDR_ENV=1`)
runs:

```sh
herdr notification show 'Concierge' --body "ONE_LINE_REQUEST" --sound request
```

Use a one-line body that leads with the operator's required action, such as
"Review the completed notifier task." Outside Herdr, report that no notifier
is available and put the request in the update text. Do not use raw escape
sequences. If the command fails, report the failure in the update and continue
the update and acknowledgement workflow. Command success does not confirm
that the operator saw the alert.
