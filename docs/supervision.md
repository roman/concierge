# Supervision

## Repository integrations

Integration packages own their role-specific protocols and report policies.
The adopting repository installs and configures those instructions separately.
The operator may authorize a narrowly scoped integration responsibility for the
primary. That authority remains limited to its stated scope. Scouts retain
their read-only contract.

Activate integration changes through their source configuration and normal
generation. Start fresh native sessions when earlier instructions remain
loaded. A task assignment cannot override higher-priority native instructions.
Keep native permission checks active.

Supply worker-specific duties and report formats in the task assignment body.
The existing operator-owned `brief-include` supplies standing defaults.
Task-specific instructions take precedence over those defaults. Change operator
configuration only after approval of that exact change.

Concierge supplies generation-bound assignments and opaque saved reports.
External consumers retrieve an accepted report with `concierge task report
TASK_ID REPLY_ID --format body`. The integration owns interpretation and any
downstream storage or validation. See the [report consumer reference](tasks.md#external-report-consumers).

## Worker supervision

### Host recovery policy

The host defaults to `normal` task recovery. It restores durable task
controllers before readiness and retries pending registrations afterward.
Select `maintenance` before startup to suppress both passes and automatic
task restarts:

```sh
concierge --root ROOT --message-root MESSAGES --recipient concierge \
  host --recovery maintenance serve
```

Maintenance keeps authenticated lifecycle commands available, including
`task discard`. All primary, generation, pane absence, native process,
report, salvage, and lease checks still apply. Task launches and task supervision
starts are refused while maintenance is active. Manual pane supervision remains explicit.
The policy applies for the host process lifetime. It does not persist a per-task pause.

Use the new binary to replace an existing host without an automatic recovery
window:

```sh
concierge --root ROOT --message-root MESSAGES --recipient concierge \
  host restart --recovery maintenance
concierge --root ROOT --message-root MESSAGES --recipient concierge host status
concierge --root ROOT --message-root MESSAGES --recipient concierge \
  task discard TASK_ID --generation GENERATION --operator-approved
concierge --root ROOT --message-root MESSAGES --recipient concierge \
  host restart --recovery normal
```

Run the discard only with operator approval after checking salvage. Capture
the original native primary identity and host configuration first. Restart
preserves the roots, recipient, Herdr executable, session, and monitor values
reported by the current host. It preserves the recovery policy unless
`--recovery` changes it explicitly. An older host without a recovery field
uses `normal`. Invalid policy values fail before replacement.

`host status` reports `recovery: "normal"` or `recovery: "maintenance"`.
New clients accept older status responses without the field. Older clients
that reject unknown response fields cannot read the new host status. Use the
new binary for the entire maintenance procedure and update adopting pins to
the revision that includes this policy before activation.

```sh
concierge control --session work status worker-1
concierge control --session work interrupt worker-1
concierge control --session work start --pane PANE_ID --name worker-1 --kind codex
concierge supervise task task-1
concierge supervise pane --pane PANE_ID --name worker-1 --kind codex \
  --agent-arg resume --agent-arg THREAD_ID
concierge supervise stop task task-1
concierge supervise stop pane --pane PANE_ID
```

Task supervision holds one controller lease per task. It checks the recorded
session, pane, and live worktree. It delivers pending inbox prompts and recovers
confirmed missing workers with their exact launch arguments. Recovery records
a new generation and a durable bootstrap message. A registered agent with no
live process can require Herdr-side repair before a restart succeeds.

Before advancing a generation, recovery saves a plan for pending assignments
and individual replies already published by that worker. Those replies remain
acceptable through later restarts. Fresh publications from an old generation
remain rejected. Reply inspection reports the current worker generation for
stop and cleanup. Resolution freezes that generation in its disposition so
an exact retry still works after retirement.

Recovery delivers each unfinished assignment under a key containing its original
message ID and the target generation. The envelope retains its original body,
original message ID, and immediate predecessor ID. `task receive` adds a visible
redelivery marker and instructions to check prior effects before repeating work.
Workers reply against the new delivery ID. Caller-key retries return the existing
delivery, including handled deliveries, rather than reopening work. Unkeyed
assignments remain independent.

Every restart publishes a durable `task-restart` notice to the primary before
launch. The notice identifies the reservation, preserved replies, and replacement
assignments. An interrupted retry uses the saved plan and confirms replacements
before acknowledging their predecessors. Observation completes these obligations
before confirming a launch. An unfinished reply disposition blocks a new restart
until the primary retries its acceptance. Legacy launch reservations that already
advanced without a recovery plan cannot reconstruct their previous generation;
their notification omits that unavailable source.

Manual pane supervision uses the host session and the supplied launch arguments.
Task supervision uses the recorded session. Neither supervisor creates a pane.
A successful supervise command returns after the wakeup is ready. The host
keeps the wakeup running after the client exits. An identical start request
returns the active result. Stop cancels and joins the wakeup without closing
its worker. Stop forbids monitor flags. Explicit monitor flags on a start request
assert the corresponding host settings.

Manual supervision and raw Start reject task-bound panes. Control interrupt resolves an
agent alias to its pane before reserving the endpoint. An active wakeup blocks
raw control of its pane. Adoption reserves the task and endpoint. An identical
adoption retry can verify a matching active task worker without stopping its
wakeup. A manual wakeup blocks adoption. Restart arguments must include the
agent's resume identity when work needs to continue after a restart.

Lifecycle hooks can call `primary claim`, `primary verify`, and `primary release`
with hook JSON on stdin. These require SessionStart, Stop, and SessionEnd events,
respectively. The hook's working directory must equal the parent of the
Concierge state root. One live session owns the primary role. Repository skills
instruct secondary sessions to leave shared task supervision to that owner.
The default `concierge` command renders the root brief after state setup.
