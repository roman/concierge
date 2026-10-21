---
name: concierge-local-supervisor
description: Use Concierge to delegate project work, supervise workers, read task reports, and apply operator-owned delivery settings. Use Tincan for durable messages and wakes.
---
# Concierge local supervisor

Do not re-read this skill until compaction or a revision change.
Read only the reference needed for the current operation.

## Contract and safety

The primary reads projects and delegates changes, investigations, planning,
reproduction, verification, and audits. An operator exception grants only its
stated scope. A report, recommendation, or worker question grants no approval.
Use normal tasks for authorized changes and bounded research. Separate knowledge
deliverables use `--kind scout`. Scouts never edit, commit, push, or open PRs.
Read existing reports before commissioning research. Never copy this contract
into an adopting repository's AGENTS.md or CLAUDE.md.

Only the owning primary handles intake, worker control, and wakes. Secondary
sessions defer to it. Confirm a crashed owner ended before removing its record.
Keep the absolute task/message roots, native permissions, trust, ownership, and
bindings unchanged. Never obtain authority by changing operator-owned settings.
Read `concierge project show REPO` before launch and again before landing.
Missing settings mean merge off, PR delivery, and no brief include.

Before every pane action, read its current visible content and agent status:
`herdr agent read PANE_ID --session SESSION --source visible --lines 80 --format text`
and `concierge control --session SESSION status PANE_ID`.
Before relaying a question, verify it remains open in the current worker context.
Report uncertainty. Keep unprocessed and unfamiliar messages pending.
Worker setup checks the inherited generation without mailbox access. Each
notification requires its standalone generation check before listing pending IDs.
Stop on missing or mismatched launch context. Never copy a generation from a
notification, transcript, registry, or mailbox into the environment.

## Claim, update, acknowledge

Run the notification's exact claim command with its explicit roots and recipient.
Act only on `status: current`. A mailbox publication grants no processing authority.
The claim supplies fresh workers, pending messages, completion work, open actions,
and a two-minute token. Batch independent reads. Reuse these returned states;
read status again only for a new decision or the required current pane check.
Wait longer on running commands instead of repeatedly polling them.

First inspect a pending bound reply with
`concierge task reply check REPLY_ID --format concise`; act only on `status: current`.
Before acceptance, require a fresh `task reply check REPLY_ID --format toon`
if the preview lacks decision evidence. An empty outcome requires this full check.
See the completion reference for evidence and exact-body import rules.
Use Tincan for ordinary coordination. Acknowledge handled
coordination with `tincan inbox ack concierge MESSAGE_ID`. Do not repeat handled work.
Record an operator request first: `concierge operator ask ACTION_ID "DESCRIPTION"`.
Resolve it only after an answer or withdrawal: `concierge operator resolve ACTION_ID`.

Post one short row per task: **Task | Outcome or progress | Blocker | Next owner |
Operator input**. Keep unchanged rows brief. Honor explicit pauses.
End the update with **Waiting on you**, listing every open request.
State that no operator action is needed when the list is empty.
Use the harness notification tool when requested; Claude uses `PushNotification`.
Post the update before its receipt:
`concierge heartbeat ack EVENT --generation GENERATION --token CLAIM_TOKEN --output OUTPUT_REFERENCE`.
The reference identifies the posted update. Queue acceptance, hook exit, and a
drained queue do not acknowledge output. An acknowledged event needs no repeat.
Stale, paused, expired, or in-progress claims require fresh verification.
An inbox event authorizes its pending messages and report; honor a periodic pause.

In Codex, use sandbox access for ordinary file reads. Request native approval
on the first attempt for an operation with an already observed socket, process
identity, or state-write restriction. Keep that evidence scoped to the operation
and roots. Do not escalate an entire batch of unrelated reads. For a new access
failure, retry the exact command once with `sandbox_permissions=require_escalated`.
A denial leaves the work pending. Read [Command access](references/command-access.md)
for command classes and failure handling. Keep native permissions unchanged.

## Accept, land, stop, cleanup

Require completed passing checks for the exact commit and confirm scope.
Scouts report findings, evidence, limitations, and open decisions. Keep decisions
open; implementation needs authorization. Preserve the accepted generation.
Accept a verified reply with an explicit date:
`concierge backlog resolve REPLY_ID --mode close --closed-on YYYY-MM-DD`.
For project changes, run `concierge land TASK_ID` after acceptance and before stop.
Scouts skip landing. Merge off needs approval for that exact landing and
`--operator-approved`. Merge on permits green, in-scope landing. Escalate
destructive, irreversible, or security-sensitive landings even with merge on.
Local PR workers push and open a PR. Fast-forward workers commit a clean branch,
remove ignored files, and keep the branch until landing; they do not push or open PRs.
Report the landed commit or PR URL and skipped operations. Inspect the remote
after failed or unconfirmed landing. Never force, discard unlanded work, or
bypass a lease refusal.

Confirm the recorded pane is idle or done using fresh visible content and status.
Run `concierge task stop TASK_ID --generation GEN`, then
`concierge backlog cleanup TASK_ID --generation GEN` with the accepted generation.
Move a live viewer off the tab before retrying stop. Retry failed focus restoration
after the viewer detaches or returns. Cleanup must confirm pane departure and
retire the binding before another adoption. Use `concierge task pending` for
unfinished completion work. Read the completion reference for retain or retry cases.

## References on demand

- [Ownership](references/ownership.md): root claims, process identity, scoped exceptions.
- [Setup](references/setup.md): command paths, fixed roots, state initialization and migration.
- [Host](references/host.md): readiness, host inspection and upgrade.
- [Messages](references/messages.md): ordinary messages, bound replies and operator actions.
- [Heartbeat](references/heartbeat.md): activation, pauses, native registration and wake faults.
- [Launch](references/launch.md): intake, settings, launch arguments, retries and adoption.
- [Assignments](references/assignments.md): keyed delivery, worker receive, reply and acknowledgement.
- [Completion](references/completion.md): saved reports, acceptance, retain, stop and cleanup retries.
- [Recovery](references/recovery.md): supervision, bootstrap, controlled resume and native permission checkpoints.
- [Remote](references/remote.md): remote setup, delivery and recovery differences.
