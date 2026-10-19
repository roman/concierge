# Concierge

[![CI](https://github.com/roman/concierge/actions/workflows/ci.yml/badge.svg)](https://github.com/roman/concierge/actions/workflows/ci.yml)

Concierge manages local tasks, coding workers, results, and supervision.
[Tincan](https://github.com/roman/tincan) supplies durable messages.
[Herdr](https://github.com/herdrdev/herdr) runs the workers.
[Treehouse](https://github.com/kunchenguid/treehouse) supplies worktree leases.

## Install

Add Concierge to a flake using flake-parts and devshell-modules:

```nix
inputs.concierge = {
  url = "github:roman/concierge";
  inputs.nixpkgs.follows = "nixpkgs";
  inputs.devshell-modules.follows = "devshell-modules";
};

# Inside the flake-parts configuration:
perSystem = { ... }: {
  devenv.shells.default = {
    imports = [ inputs.concierge.devenvModules.default ];
    concierge.enable = true;
  };
};
```

The module provides the commands, root hooks, and supervisor skill.
Commit the native hook settings described in the [shell guide](docs/development-shell.md).
`devShellModules.default` remains an alias for one release.
For commands alone, run `nix profile install github:roman/concierge#concierge`.
The `tincan` package output is also available.

## Use

Enter the project shell and initialize fresh state:

```sh
nix develop
concierge state init
concierge
```

Start a root Claude or Codex session inside that shell. The root hooks claim
primary ownership and start the host. Ask the primary to queue and launch work.
Inspect progress with `concierge backlog list` and `concierge worker list`.
Existing state requires the [migration procedure](docs/state.md).

## Remote workers

Register an SSH machine with Herdr once. Concierge supports Apple Silicon macOS
remotes. Install Nix with flakes, Herdr, and the selected agent on the remote.
Choose remote repository keys or SSH agent forwarding to a trusted machine.
Complete the agent login on the remote by hand.
Verify the SSH host keys on both machines before running unattended commands.

Declare the saved machine ID, its SSH target, a private remote base directory,
the agent, and repository URLs in `config/machines.json` under the state root.
Include the `concierge` repository and every project or dependency repository.
See the [remote setup guide](docs/remote-setup.md) for the configuration and
first launch. The [remote architecture guide](docs/remote-architecture.md)
explains the components and execution flow with Mermaid diagrams.
Set `authentication` to `agent-forwarding` to use keys loaded in the primary's
SSH agent. Omit it or select `remote` to use the remote's own credentials.

```sh
concierge machine check baker
concierge machine prepare baker --client-source /path/to/clean/concierge
concierge machine status baker
```

Preparation builds the committed client with the remote Nix installation.
Launch a queued task with the usual launch arguments plus `--machine baker`.
The primary owns supervision, result acceptance, landing, and lease cleanup.
It imports a ship worker's immutable commit into a primary worktree before
acceptance. Fast-forward delivery needs no remote push. PR delivery publishes
from the primary under the existing merge authority.

`worker show TASK_ID` reports Herdr and tunnel reachability separately.
An unreachable worker keeps its generation, lease, and unsent reply. A revoked
secret stops remote retries. Preserve its outbox record and use the trusted
primary command `concierge task reply reconcile --file RECORD` at the original
root. Keep the original generation, assignment, reply key, recipient, and body.
The guide explains recovery and cleanup checks.

Run the optional acceptance workflow against a saved Apple Silicon macOS
machine from a native primary session:

```sh
bash scripts/acceptance-remote.sh baker /tmp/concierge-remote-report.md
```

Replace `baker` with the saved machine to test. Ordinary integration tests do
not connect to a remote machine. The workflow requires committed, clean source,
a loaded primary SSH agent, and an existing Codex login on the remote. It uses
temporary state roots and an owned remote directory. It starts real scout and
ship workers with native permissions, restarts its temporary primary, drops and
recovers its tunnel, and lands into a temporary local Git origin. It does not
push to the source repository. Existing Herdr and Codex workflow directories
receive their normal runtime records. The report path must not exist.
Acceptance workers explicitly select Codex's `workspace-write` sandbox and keep
the existing native approval policy. They select `approvals_reviewer=auto_review`
through launch arguments so eligible requests receive automatic review, as in
local development. This setting does not disable the sandbox or approvals and
does not change user configuration. The task directory is an additional writable
root for the result and transport files. Resolve remaining native trust or
approval dialogs in the remote pane named in the run output. The run waits up to
twelve minutes for startup or a worker reply.

## Develop

Copy `.envrc.template` to `.envrc` for optional direnv setup, then run
`direnv allow`. Otherwise use `nix develop`.
The shell and reusable Concierge module include Nix Python for Herdr's native
session hook. On macOS, Apple's Python launcher fails under the Nix SDK
settings. Project shells configured without the module must also declare
Python 3 when running native Codex workers.

```sh
nix fmt
nix flake check
```

CI checks Linux and macOS. The flake exports packages and shells for both
architectures on each platform.

## Reference

- [Commands and project settings](docs/commands.md)
- [Task lifecycle and lease cleanup](docs/tasks.md)
- [Worker launch and native checkpoints](docs/launch.md)
- [Remote workers and reply recovery](docs/remote-workers.md)
- [Remote setup and first launch](docs/remote-setup.md)
- [Remote architecture and protocol flow](docs/remote-architecture.md)
- [Supervision and root hooks](docs/supervision.md)
- [State roots and migration](docs/state.md)
- [Development shell integration](docs/development-shell.md)
- [Architecture and package map](docs/architecture.md)
