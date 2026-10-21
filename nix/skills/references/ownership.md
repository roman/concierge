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
