# Set up remote workers

Concierge runs coding workers on a saved Herdr machine over SSH. The primary
stays on your development machine and owns task state, supervision, result
acceptance, and delivery.
`dev-remote` below is an example saved machine name; use your own machine name.

For the component and protocol design, read [remote architecture](remote-architecture.md).
For disconnects, retained replies, and cleanup, read [remote operations](remote-workers.md).

## 1. Prepare the primary and remote

On the primary, install Concierge through the [development shell](development-shell.md).
Select a Concierge revision with the remote machine commands. Remote workers
require format-three state. Fresh `state init` creates version-two state, so
upgrade it before starting a host or launching workers. From the project shell,
with a new backup path outside the state root:

```sh
concierge state init
concierge state upgrade --backup /absolute/path/concierge-before-remotes
concierge
```

For existing state, follow the full
[remote state upgrade procedure](state.md#upgrade-local-state-for-remote-records).
It requires retiring workers and stopping state writers first. Start a native
primary session through the project's root hooks after the upgrade.
The primary must hold an active ownership claim before machine preparation or
worker launch.

On the remote, install these tools declaratively through your Nix configuration:

- Nix with flakes enabled.
- Herdr 0.9.5 or newer, with a compatible server already running.
- The selected native agent, Codex or Claude.
- The project's development tools and dependencies.

The remote client supports x86_64 Linux, aarch64 Linux, and Apple Silicon macOS.
`concierge machine check` verifies compatibility before preparation. Native
acceptance has passed on x86_64 Linux and Apple Silicon macOS.

Nix and Herdr must be available to non-interactive SSH commands. Concierge does
not install or replace the remote Herdr server. Project shells that run native
Codex workers also need Python 3 for Herdr's session hook. The Concierge shell
module declares this dependency; custom shells must declare it themselves.

Complete the agent login on the remote:

```sh
# Choose the agent you intend to run.
codex login
codex login status

# Or:
claude auth login
claude auth status
```

Concierge preserves native agent permissions and login configuration. Resolve
folder trust and other native dialogs on the remote when they appear.

## 2. Register the SSH machine

Configure SSH login from the primary and verify the remote host key. Use an SSH
host entry without existing `LocalForward`, `RemoteForward`, `DynamicForward`,
or `Tunnel` settings. Concierge manages its own socket forwards.

Register the machine with `herdr machine add`, then inspect the saved profile:

```sh
herdr machine list --json
```

Keep the stable machine ID and SSH target from that profile. The configuration
key in the next step is the stable ID. The `target` value is its saved SSH target.
Herdr owns the machine registration and remote session selection.

## 3. Choose repository authentication

| Mode | Where repository credentials live | Operator setup |
| --- | --- | --- |
| `remote` (default) | Remote machine | Provision repository credentials there; a read-only deploy key is sufficient. |
| `agent-forwarding` | Primary SSH agent | Load the key on the primary and let the Concierge host inherit `SSH_AUTH_SOCK`. |

In either mode, verify the Git server's host key on the remote. Check repository
access there with `git ls-remote -- URL`. Preparation checks access without
interactive prompts. Concierge does not provision keys or copy agent logins.

For forwarding, load and inspect the primary's agent before starting the host:

```sh
ssh-add /path/to/repository-key
ssh-add -l
```

An agent started in another terminal does not update an already running host's
environment. Ensure the host inherits the working socket. A missing agent or an
agent without usable keys blocks preparation.

Choose forwarding only for a trusted remote and trusted worker processes.
Processes with access to the remote agent socket can request authentication
with your loaded keys. Private keys and passphrases stay on the primary.
Workers receive a fixed socket in their private generation directory, which
survives tunnel reconnection without changing the worker's environment.

## 4. Declare the machine and repositories

Create `config/machines.json` under the Concierge state root, normally
`.concierge/config/machines.json` in the adopting project:

```json
{
  "SAVED_MACHINE_ID": {
    "target": "dev-remote",
    "base": "/Users/developer/concierge",
    "agent": "codex",
    "authentication": "agent-forwarding",
    "repositories": [
      {"name": "concierge", "url": "git@github.com:roman/concierge.git"},
      {"name": "my-project", "url": "git@github.com:owner/my-project.git"}
    ]
  }
}
```

Replace the profile ID, target, paths, and repository URLs. Use an absolute,
private remote base directory that is empty before first preparation. Include
the Concierge repository, the task repository, and every dependency repository.
Repository names match the names used by `backlog add --repo`. The primary
project's `origin` must match its declared URL.

Omitting `base` selects `$HOME/.local/share/concierge` on the remote. Keep the
same base between runs. Machine checks and preparation resolve it to its
canonical physical path without creating directories during the check.
For example, a macOS `/tmp` prefix resolves to `/private/tmp`. Launch records
the resolved destination. Cleanup uses that captured destination.

Omit `authentication` or select `remote` for credentials stored on the remote.
Forwarding requires an explicit selection for each saved machine.

## 5. Check and prepare

Run these commands from the primary's project shell and state root:

```sh
concierge machine check dev-remote
concierge machine prepare dev-remote --client-source /path/to/clean/concierge
concierge machine status dev-remote
```

`check` reports missing prerequisites and the corresponding operator action.
`prepare` creates owned repository checkouts under the remote base. It clones
or fetches their declared origins. It transfers a Git bundle from the clean
Concierge source checkout's committed HEAD and builds that client with remote
Nix. Unpublished commits can be prepared without pushing them. Local Git
configuration and uncommitted files are excluded from the transfer.

The source checkout's origin must match the declared Concierge URL. Repeating
preparation reuses owned directories. A foreign directory or a changed origin
stops preparation. `status` verifies the prepared client executable as well as
the machine setup.

The packaged client includes Treehouse. The first task allocates a slot under
`<base>/worktrees/.treehouse/<repository-id>/<slot>/<repository>`. Returning a
clean, quiet lease releases the slot for the next task at the same canonical
path. Keep the prepared base and pool between launches. Removing the base or
changing it creates new slot paths. Unwrapped clients without Treehouse use a
legacy Git fallback with a different path for each lease.

For Codex, `machine check` lists every existing slot and its trust status. It
reads the documented native `config.toml` project entries without starting an
agent or changing configuration. An untrusted slot includes the exact
`codex -C '<slot-path>'` command for the operator's one-time folder-trust
decision. The first launch can allocate a new slot before any slots exist.
Each additional concurrent slot requires its own decision.

Codex keys trust by the exact working directory or a recognized repository
root. A trusted parent base does not cover this bare-repository pool. The
contract is one native decision per stable slot. See the
[source evidence](../../exmachina/vault/projects/concierge/artifacts/2026-10-18-remote-hardening-trust-evidence.md)
and the [native configuration reference](https://developers.openai.com/codex/config-reference/).
Concierge never writes native trust settings or answers a trust dialog.

## 6. Launch a task

Queue a task through the normal [task workflow](tasks.md). Add `--machine` to
the usual [launch command](launch.md):

```sh
concierge launch task-1 --machine dev-remote --harness codex \
  --agent-arg=--sandbox=workspace-write \
  --project /absolute/primary/my-project --branch task/task-1 \
  --base-commit FULL_COMMIT_ID --body-file brief.txt
```

Use the primary project path, a full commit ID, and a new task branch. Concierge
selects the remote checkout and launches the agent through Herdr. The recorded
launch captures the machine, authentication mode, and prepared client revision.
An interrupted launch must retain those settings on retry.

New remote Codex launches require an explicit
`--agent-arg=--sandbox=workspace-write`. Concierge rejects missing or incompatible
modes before it prepares a lease or reserves a launch. Native default read-only
permissions cannot accept the task directory supplied through `--add-dir`.
Concierge preserves an existing captured launch's arguments during recovery.
Local launches and Claude launches keep their existing settings.

Codex keeps its configured approval reviewer by default. The named launch
option `--codex-approval-reviewer=auto-review` explicitly selects native
automatic review for one remote Codex launch. Its default, `native`, preserves
the current settings. The selected arguments remain captured for exact retries
and worker recovery. Existing raw `--agent-arg` settings remain supported.

Automatic review evaluates eligible approval requests. It can reject a request
or require operator input. It grants no exact Unix-socket allowance and never
answers a folder-trust dialog. The sandbox remains active. The option writes
no native configuration. Remaining dialogs appear in the recorded remote pane.

Inspect the worker with `concierge worker show task-1`. Accept its result through
the normal task workflow, then use `concierge land task-1` under the project's
merge authority. For remote workers, the primary imports and publishes the
selected commit in both fast-forward and PR delivery modes. The remote needs
no repository write credentials.

## Optional end-to-end acceptance

From a native primary session in the clean, committed Concierge checkout:

```sh
bash scripts/acceptance-remote.sh dev-remote /tmp/concierge-remote-report.md
```

The report path must not already exist. The workflow needs a loaded primary
SSH agent and an existing remote Codex login. It creates isolated state and
owned remote directories, runs real scout and ship workers, tests primary
restart and tunnel recovery, and lands into a temporary local Git origin.
It does not push to the source repository. Resolve native trust dialogs in the
pane reported by the run. See the [README](../README.md#remote-workers) for
the acceptance worker's native approval settings and runtime effects.

## Use a local VM as the remote

A VM on the primary machine uses the same SSH transport and worker protocol as
a separate physical machine. Provision its Nix installation, Herdr service,
agent, and development tools through its NixOS configuration. Run Herdr as the
development user's service and enable lingering so it stays available between
SSH sessions. Complete the agent login inside the VM.

Forward an SSH port to the guest on the primary's loopback interface. For
example, a guest SSH service forwarded to `127.0.0.1:22222` can be registered as:

```sh
herdr machine add ssh://developer@127.0.0.1:22222 --label dev-remote
```

Verify the guest's host key through the VM console or another trusted channel
before unattended SSH access. Authorize the primary's SSH public key in the
guest. Repository access still uses either remote credentials or explicitly
selected agent forwarding.

Use the saved profile's exact SSH target in `config/machines.json`, such as
`ssh://developer@127.0.0.1:22222`. Select a private guest base path such as
`/home/developer/concierge`, then follow the same check, prepare, launch, and
acceptance steps above. The VM must remain running while tasks hold remote
leases. Retire those workers before stopping or deleting the guest.
