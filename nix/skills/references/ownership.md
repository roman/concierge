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

## Primary session boundaries

Request a fresh primary session at a safe task boundary to limit context age
and token cost. Only evaluate this boundary during a fresh `status: current`
heartbeat claim. Defer without one. Require zero bound workers and no unbound
launches, pending messages, completion work, or open actions. Also read
`concierge backlog list` and defer while queued work remains. Defer while any
other work, approval, or operator question is pending, or the operator is
mid-conversation.

At that boundary, record a durable handoff with the outcome, pending work,
branch state, and next action. Use the adopting project's handoff location.
Tell the operator a fresh primary session is due and name the reason and exact
handoff path to supply to the new session. After a Concierge pin upgrade, give
the same notice at a safe boundary because the current session retains a
stale PATH binary.

The operator starts the new session. Never end or restart your own session.
Keep ownership, bindings, and heartbeat duties unchanged while waiting.
Post any required heartbeat update before its acknowledgement. The new session
follows normal ownership checks and reads that handoff path and a fresh
`concierge brief`. Claim the next native event when it arrives. Resume from
these durable records and fresh state instead of the old conversation.
