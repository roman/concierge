# Supervision

## Primary session continuity

The source supervisor protocol in `nix/skills/SKILL.md` requires a session
handoff from the beginning of each non-trivial primary session. The devshell
generates adopting repositories' skill copies from that source. Change the
source and upgrade the adopting pin to distribute protocol updates.

The `concierge` vault project owns the cross-project activity handoff. A primary
running in a vault repository still records its supervision under `concierge`.
Project worker reports retain their own project ownership. The session handoff
connects task IDs, saved report IDs, decisions, delivery state, blockers, and
open operator actions across those projects.

The primary keeps a recoverable snapshot and persistence receipt under ignored
`$CONCIERGE_HOME/session-handoffs/`. This is supervisor state. A separate vault
worker owns all vault writes, validation, and commits. The primary lands that
worker's result through the vault project's delivery and merge settings.

Follow the [source protocol's handoff procedure](../nix/skills/SKILL.md#concierge-session-handoff)
for startup recovery, explicit vault selection, update triggers, and serialized
persistence. Recover local pending snapshots even when an earlier vault write
failed. Import creates the first note. Subsequent updates edit the same note
and preserve its full metadata. Vault worker replies are operational receipts;
importing those receipts would cause recursive vault work.

Locker keeps a session handoff `current` while its body can describe a pending
outcome. It does not have a `pending` frontmatter status. A later native session
creates a new handoff after recovering prior context. The vault worker preserves
still-current guidance before superseding affected notes and linking their
replacement. The initial handoff, checkpoint updates, and final report follow
one lifecycle. A restart is not a prerequisite for creating the initial note.

For example, an operator correction that changes delivery from a PR to a local
landing belongs in the session snapshot at the next checkpoint. Record the
decision, affected task, and next owner. After landing, update the same handoff
with the actual commit and remaining cleanup. Import the project's accepted
worker report separately through the [task report flow](tasks.md#import-accepted-notes-through-a-vault-worker).

## Worker supervision

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
