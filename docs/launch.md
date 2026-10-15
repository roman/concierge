# Worker launch


Queue the task first, then ask the host to lease a treehouse worktree and
launch its worker:

```sh
concierge launch task-1 --harness codex \
  --project /repo --branch task/task-1 \
  --base-commit FULL_COMMIT_ID --body-file brief.txt
```

By default, Concierge creates a task-owned workspace and runs the worker in its
first pane. The reserved workspace label supports recovery after a lost creation
response. Stop closes only the recorded worker pane. Herdr removes the workspace
when its last pane closes; any additional user panes keep the workspace open.
Cleanup then records the retired task. Existing shared workspaces remain
available through `--workspace WORKSPACE_ID`; labels are unsupported.

New workspace labels and worker pane titles use `<repo>/<name>`. The name is
the task ID with a leading `<repo>-` removed. For example, `tincan-cleanup`
appears as `tincan/cleanup`. Controlled restarts report the same pane title.
The agent identity remains `c-<generation>`. Existing workspace labels stay
recorded and remain valid for launch recovery and cleanup.

Treehouse is a required host dependency and is included in the Concierge Nix
package. The host selects the leased path. Fresh requests omit `--worktree`;
that option exists only for recovery of an already recorded raw-worktree launch.
The lease must be clean. Concierge creates the new task branch at the exact
base commit. Changed files, ignored-file conflicts, and existing branch conflicts
stop preparation without a forced reset.

Acquisition identity is recorded before requesting a lease. An uncertain request
recovers its existing lease by holder instead of acquiring another worktree.
Launch and restart verify the captured lease ID, holder, and path before native
startup. The task record retains the lease identity and result branch through restart
and retirement. A changed or missing lease requires inspection.

Use a full commit ID from the project and a new branch name. The host must have
an explicit Herdr session. `--harness claude` selects Claude. Repeat
`--agent-arg=VALUE` for optional launch arguments in their original order.
`--body-file -` reads the assignment from stdin. Claude arguments must not contain
`--settings`. Codex arguments must not override `shell_environment_policy`.
Configure other harness settings through the harness's user or project settings.

Initial launch and native resume load the worktree environment after pane shell
startup. A worktree with `.envrc` uses `direnv exec WORKTREE`. Otherwise, a flake
with a default devShell for the host system uses `nix develop WORKTREE -c`.
Repositories without either keep the native launch. Flake evaluation errors stop
launch instead of silently starting outside the environment. Add
`.envrc.template` for optional local direnv setup; a fresh worktree needs no
`.envrc` when its flake provides the default devShell.

Devshell launches pass a 120-second startup timeout to Herdr. Cold evaluation
and shell hooks run inside that allowance before the native agent becomes ready.
Native launches without a devshell keep Herdr's default timeout. A timeout can
release the requested agent name while the pane process continues running.

The primary heartbeat and `concierge task pending` report an unbound launch in
`agent_requested` after two minutes from its recorded request time. The report
includes the session, pane, generation, deadline, and an abandonment hint.
The two-minute reporting threshold applies to all initial launches. Older
records without a request time appear immediately with an unknown deadline.
Inspect the named session and pane before closing verified launch resources.
Then use the reported abandonment command. The report does not stop a pane or
retry a launch.

This repository's `.mcp.json` starts `gopls mcp` for Claude through direnv when
`.envrc` exists, or through the default Nix devshell otherwise.

The host validates the request before reserving it. It captures the native
session ID from Herdr, adopts the worker, publishes its assignment, and registers
the task wakeup. Missing native session metadata leaves the launch unresolved.
Worker prompts contain the captured command paths, roots, recipient, and a check
of the inherited generation. They contain no assignment body.

The host continues tracked initial launches and retries the first wakeup
registration without a model request. A host restart reconstructs this work from
task records. A supervision stop removes the task from periodic recovery for the
current host lifetime. A later explicit retry can resume the same pending launch.
An active wakeup's failure does not create a fresh automatic restart budget.

Recovery retires an unadopted launch when its task is held or Herdr reports
that its workspace no longer exists. Before retirement, the host checks that
the launch's agent, creation label, and any recorded tab or pane are absent
across the session. A failed or incomplete inventory leaves the intent pending.
Confirmed absence ends recovery on the first attempt. The task retains an
abandonment receipt and its worktree metadata. The host logs one retirement
line. Later reconciliation and host restarts skip that intent. A deleted task
record ends pending recovery with one diagnostic and is never recreated.

To clean up stale intents left by an older host, install the updated Concierge
binary and restart the host. Startup performs the same retirement pass:

```sh
concierge --root /path/to/.concierge --message-root /path/to/.tincan host restart
```

Inspect `abandoned_launches` in `ROOT/records/TASK_ID.json` for the retained
receipt. Cleanup preserves the task hold, worktree, and branch. It does not
remove worker resources.

Exact pending retries must preserve every request field. A completed or retired
launch requires inspection instead of another launch. A disconnected client
cannot establish whether launch completed. Inspect `backlog show TASK_ID` and
`worker show TASK_ID` before issuing another launch. The client never retries
automatically. Uncertain pane or agent creation is discovered without submitting
another creation attempt.

## Native checkpoints

A blocked initial worker remains an unadopted launch intent. Concierge reports
`checkpoint: folder_trust` for a recognized Claude workspace trust dialog and
`checkpoint: native_input` for other blocked input or unavailable screen text.
The response names the recorded session, pane, and worktree and reports whether
the primary notification was confirmed. It requires an operator decision in the
existing native dialog. After the dialog is resolved, retry the exact launch;
background reconciliation also continues the saved intent without creating a
second worker.

The host publishes a durable `task-native-checkpoint` message to its configured
primary, including checkpoints first observed during background reconciliation.
Retries retain one message per task generation and checkpoint kind. A generic
checkpoint can receive a separate folder-trust classification notice after a
screen read recovers. Native screen text is used only for bounded diagnostics;
it is never forwarded as instructions or used to approve a dialog. Concierge
keeps existing trust, permission, and sandbox settings intact. Task holds and
identity failures remain separate launch blockers.

## Initial launch state

The task-state API preserves an initial launch request inside its authoritative
task record. Its phases are `reserved`, `preparing`, `lease_requested`,
`lease_ready`, `pane_requested`, and `agent_requested`. Each new reservation needs a fresh generation. Exact retries
retain that generation and request. Every progress transition checks the expected
generation. A rejected or uncertain write must stop the executor before further
external effects.

The shared adoption lock reserves worker names, worktrees, branches, creation
labels, and captured endpoints across tasks. Endpoint and native resume identity
are write-once. Only `reserved` intent permits cancellation without resource
reconciliation. A hold prevents new preparation or launch effects. Already-created
resources can still be recorded during a hold.

Intent remains alongside matching adoption and in-flight records. The caller
must confirm durable assignment publication before clearing it. Results and
ordinary restart are blocked during initial launch finalization. A potentially
submitted pane creation requires reconciliation; an empty lookup after a timeout
does not permit another creation attempt.
