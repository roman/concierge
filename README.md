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

## Develop

Copy `.envrc.template` to `.envrc` for optional direnv setup, then run
`direnv allow`. Otherwise use `nix develop`.

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
- [Supervision and root hooks](docs/supervision.md)
- [State roots and migration](docs/state.md)
- [Development shell integration](docs/development-shell.md)
- [Architecture and package map](docs/architecture.md)
