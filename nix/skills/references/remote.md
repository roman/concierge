# Remote workers

Read the repository's [remote setup guide](https://github.com/roman/concierge/blob/main/docs/remote-setup.md)
before preparing a machine. Read [remote operations](https://github.com/roman/concierge/blob/main/docs/remote-workers.md)
for connection, result import, outbox reconciliation, and cleanup procedures.
Use the guides from the pinned Concierge revision when available locally.

Remote launches add `--machine SAVED_MACHINE` to the normal launch command.
Keep native permissions and login settings active. Operator approval requests
remain native dialogs on the remote. Never answer a permission dialog through
pane supervision.

For a remote Codex worker waiting at an approval dialog, check the remote
host's Codex approval reviewer with the operator. The launch default
`--codex-approval-reviewer=native` preserves that host's configuration.
The operator can set `approvals_reviewer = "auto_review"` in
`~/.codex/config.toml` on the remote host. A future launch can select
`--codex-approval-reviewer=auto-review` explicitly. Automatic review keeps
native permissions, trust, and roots active. Requests that still need operator
input remain native dialogs. A `task-stalled` notice includes this advice for
remote Codex workers. Preserve the current assignment and generation while
the operator corrects the cause. Never answer the dialog through supervision.

Read the component table and version warnings from `machine check`,
`machine status`, and launch output. Relay each warning and repair step to the
operator. The comparison uses the running primary host's commit, rather than
the invoking CLI's commit. A plain version difference permits launch. An
explicit incompatible Herdr protocol stops it before resource creation.
Save work and obtain approval before restarting a stale Herdr server.

Remote delivery differs from local worker delivery. The remote worker commits
its result and removes ignored files. Acceptance imports the selected commit
into a primary worktree. The primary runs `concierge land TASK_ID` with the
same operator-owned merge authority and delivery gates. The primary publishes
a remote task's PR. The remote worker needs no repository write credentials.
Export can proceed with ignored devshell outputs present. It preserves those
outputs and checks the owned lease, reserved branch, tracked content, untracked
content, and selected HEAD. Only the committed result enters the bundle.
Lease return still requires cleanup and process checks. The primary reads the
current operator-owned `return_ignored_paths` list from `config/projects.json`
on each attempt and sends it through the trusted return transport. The remote
removes only matching ignored entries after lease, immutable commit, and process
checks pass. It preserves and names unmatched ignored files. A worker request,
task brief, or remote-side file cannot authorize deletion. An empty or missing
list keeps the existing return policy. After the operator changes the list,
retry cleanup for the stopped task without a relaunch. Preserve pre-existing
or unknown ignored files and report them for inspection before lease return.

An unavailable SSH connection does not authorize a replacement worker or free
its lease. Inspect `concierge worker show TASK_ID` and preserve the generation.
Retry an unconfirmed reply with the identical key, arguments, and body.
A revoked secret requires the trusted primary's retained-outbox reconciliation
procedure. Never copy a generation into the worker environment to bypass it.

Cleanup must confirm agent and worktree quiescence. Unknown ownership, active
listeners, dirty files, or unconfirmed lease returns require inspection.
Do not remove arbitrary sockets or reset worktrees to make cleanup pass.

## Worker development shells

Read the operator-owned project settings before launch. For flakes that require
impure evaluation, ask the operator to configure
`concierge project set REPO --nix-develop-arg=--impure`. The saved arguments apply
to local and remote launches and resumes. Do not seed an untracked `.envrc`.
An existing `.envrc` retains its own `direnv exec` policy.
