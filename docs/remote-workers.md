# Remote workers

One primary host manages local and remote workers. A remote worker uses its own
checkout and calls the primary through a separate, task-scoped worker socket.
The primary host socket stays local. Local workers keep their existing paths.

## Operator setup

Register the remote with `herdr machine add`. Read its stable ID and SSH target
with `herdr machine list --json`. The remote Herdr server must already run a
compatible version. Concierge does not replace that server.

On the primary, verify the saved SSH target's host key and configure SSH login.
On the remote, install Nix with flakes and Herdr 0.9.5 or newer. Make both tools
available to non-interactive SSH commands. Install the chosen agent. Run either
`codex login` and `codex login status`, or `claude auth login` and
`claude auth status` on that machine.

Choose the repository authentication mode for each saved machine.
The default `remote` mode uses credentials provisioned on the remote.
Provision a read-only deploy key for each private repository in that mode.
Verify the repository server's host key there. Test each URL with
`git ls-remote -- URL`. Concierge checks this access without interactive prompts.
It does not create or copy repository credentials or agent logins.

For a trusted development machine, select `agent-forwarding` instead. Load and
unlock the repository key in the primary's SSH agent with `ssh-add`. The primary
Concierge process must inherit a working `SSH_AUTH_SOCK`. Checks and preparation
forward the agent for their SSH connections. Workers use a fixed agent socket
inside their private generation directory. The host owns that socket forward
alongside the worker protocol tunnel and recovers it after a disconnect.

Private keys and passphrases stay on the primary. Processes with access to the
remote socket can request authentication with the agent's keys. Select this mode
only for a machine and worker processes you trust with those capabilities.
Repository host-key checks still apply on the remote. A missing or empty primary
agent stops preparation. Lost agent access preserves the worker and its lease.
Restore the primary agent and its loaded key, then allow the host to retry.

Use a format-three state root and an active primary claim. Follow the existing
[state migration procedure](state.md) for an older root. Create
`config/machines.json` under that root:

```json
{
  "SAVED_MACHINE_ID": {
    "target": "baker",
    "base": "/Users/roman/concierge",
    "agent": "codex",
    "authentication": "agent-forwarding",
    "repositories": [
      {"name": "concierge", "url": "git@github.com:roman/concierge.git"},
      {"name": "my-project", "url": "git@github.com:owner/my-project.git"}
    ]
  }
}
```

Replace the ID, target, base, and URLs with the saved profile and declared
repositories. The base must be an absolute remote path. Choose an empty directory
for first preparation. Concierge records its machine owner and refuses a foreign
directory or changed repository origin. Include every dependency repository.
Omit `authentication` or set it to `remote` to use remote repository credentials.
Forwarding is never enabled implicitly for another saved machine. A launch
captures its authentication mode with its destination and immutable client.
An interrupted launch refuses a retry that changes those captured settings.

```sh
concierge machine check baker
concierge machine prepare baker --client-source /path/to/clean/concierge
concierge machine status baker
```

`check` reports missing prerequisites and the exact operator step for each.
`prepare` clones or fetches repositories under the base. It transfers a Git
bundle from the clean primary Concierge checkout's committed HEAD, then builds
that revision with the remote Nix. Unpublished source commits require no push.
The checkout's origin must match the declared Concierge URL. Local Git
configuration and uncommitted files are excluded. Repeated preparation reuses
the owned directories. `status` also verifies the prepared client executable.

## Launch and delivery

Queue a task as usual. Add `--machine baker` to the existing `concierge launch`
command. Supply the primary project path and full base commit ID. Concierge
creates a remote worktree with Treehouse when available, or Git otherwise.
It binds the saved machine identity and starts the agent through Herdr.
The remote agent keeps its native permissions and login configuration.

The host creates a random per-generation secret. It copies that secret through
SSH stdin into a private remote task directory. A reverse SSH tunnel connects
the remote worker socket to the primary's restricted worker socket. Task
instructions contain remote paths. The secret never appears in those
instructions, process arguments, logs, or Git.

A scout retains the existing read-only contract. A ship worker commits its
changes and removes ignored files before reporting a clean result. Acceptance
imports that exact commit into a primary worktree sharing the project Git common
directory. The primary holds both result resources until cleanup succeeds.
`worker show TASK_ID` includes the selected commit and primary lease recovery
state. An interrupted import retries the same selection and holder.

Run `concierge land TASK_ID` after acceptance. Merge authority and delivery mode
still come from `project show REPO`. Fast-forward delivery pushes the verified
commit from the primary. PR delivery creates or reuses the matching PR from the
primary and applies the existing checks and squash-merge rules. Remote workers
do not need repository write credentials. A changed selected HEAD or conflicting
published branch stops landing.

## Connection and reply recovery

`concierge worker show TASK_ID` reports Herdr and tunnel reachability separately,
including retry times. A failed probe means unreachable. It does not free a
lease, change a generation, or consume the worker's restart tolerance. The host
persists tunnel ownership and reconnect state across a primary restart.
Do not start a replacement worker solely because SSH is unavailable.

The remote client stages the complete reply before sending it. It records
publication, archive, and assignment acknowledgement independently. Retry an
unconfirmed reply with identical arguments and body. A lost confirmation can
refer to a reply already published. Changing its key can publish another reply.

A revoked secret stops network retries. Preserve the retained outbox JSON file
reported by the reply command. The outbox sits in the generation directory,
outside the worktree. Transfer that record privately to the primary without
editing it. At the original root, the trusted primary runs:

```sh
concierge task reply reconcile --file /private/path/original-record.json
```

Reconciliation uses the original root, task, generation, assignment ID, reply
key, recipient, and body. It does not grant the old worker new access. Keep the
record until publication, archive, and acknowledgement are confirmed. Never
copy a generation from a message into a worker's environment to bypass a mismatch.

## Cleanup

Cleanup revokes the secret, closes the owned tunnel, and waits for confirmed
agent and worktree quiescence. A remote worktree must be clean, including ignored
files. Changed work requires evidence that its selected commit landed, or an
explicit abandonment through the existing retained-result release procedure.
Squash delivery checks the matching merged PR and its landed merge commit.
Cleanup also returns the held primary result lease.

Unknown ownership, an active listener, dirty files, or an unconfirmed lease
return keeps the recorded resources for inspection. Do not remove an arbitrary
socket or reset a worktree to make cleanup pass. Unsent outbox records survive
worktree cleanup. See [task lifecycle](tasks.md) for retained-result release.
