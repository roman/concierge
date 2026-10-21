## Roots and setup

Resolve the repository root and set command paths in each shell call:

```sh
repository_root="$(git rev-parse --show-toplevel)"
export PATH="@commandPath@:$PATH"
export CONCIERGE_HOME="$repository_root/.concierge"
export TINCAN_HOME="$repository_root/.tincan"
concierge
```

Keep one absolute task root and message root through a task's full lifecycle.
Workers use the supplied roots, even from an isolated worktree. The primary
recipient is `concierge`. Task recipients use their task IDs. Use the merged
`concierge` and `tincan` commands. Enter the development shell before starting a root session. Nix supplies the
command paths and links the hooks and this skill. State initialization remains
explicit. Inspect an older running host with `concierge host status`.

Task records under `.concierge/records/` are authoritative. `backlog.md` is a
projection and can be absent before first intake. Use `concierge backlog list`
to check task-state access. Report version, migration, or missing-command errors
as setup gaps. Do not copy or edit a legacy backlog to initialize state. See
the Concierge README for explicit fresh
initialization and migration. Stop state writers before either operation.
