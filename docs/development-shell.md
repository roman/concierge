# Development shell integration


Concierge exports `devenvModules.default` for devshell-modules. Add both flake
inputs, import the devshell-modules flake-parts module, and configure the shell:

```nix
perSystem = { ... }: {
  devenv.shells.default = {
    imports = [ inputs.concierge.devenvModules.default ];
    concierge.enable = true;
  };
};
```

Shell entry puts the pinned Concierge and Tincan commands on PATH. It sets
`CONCIERGE_PROJECT_ROOT`, `CONCIERGE_HOME`, and `TINCAN_HOME` from `DEVENV_ROOT`
(or the shell entry directory). It links the hooks into `.agents/hooks/` and the
supervisor skill into `.agents/skills/` and `.claude/skills/`. Ignore these
generated paths in Git. The module refuses to overwrite unmanaged files or links.

Commit the native settings yourself. The module never writes them. Use these
stable command definitions in `.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-session-start; fi", "timeout": 15}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-activity; fi", "onFailure": "block"}]}],
    "Stop": [{"hooks": [
      {"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-activity; fi"},
      {"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-claude-stop; fi", "asyncRewake": true, "timeout": 28800}
    ]}]
  }
}
```

Use these definitions in `.codex/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-session-start; fi", "timeout": 15}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then ./.agents/hooks/concierge-session-end; fi", "timeout": 3}]}]
  }
}
```

Preserve unrelated native settings when adding the definitions. Review and
approve the initial hook definitions through each harness's normal trust flow.
Claude needs support for synchronous `UserPromptSubmit` failure blocking.
The native acceptance trial uses Claude Code 2.1.296. The activity hooks mark
the primary busy before a prompt and ready after a completed turn. A missing
activity hook or incompatible settings leave pull delivery unavailable.
Optional `PermissionRequest` and `StopFailure` calls to the same activity hook
record permission and unknown states. They do not answer permission requests.
A package update keeps the command definitions stable. Enter the development
shell before launching a root session. Hooks act only in that shell's project
root. The committed command guards skip worker sessions before accessing the
generated files. Pooled worker checkouts do not need module shell entry to skip
the root hooks.

Initialize fresh state explicitly with `concierge state init` before starting a
root. Shell entry only warns when state is missing. Existing state needs its
normal backup and migration procedure; the module does not migrate it.

The module preserves explicit `CONCIERGE_HOME` and `TINCAN_HOME` roots. Worker
shells skip supervisor state, host, and hook-settings checks. The module does not
install Git hooks. Repositories using devshell-modules Git hooks still need an
upstream guard against installing hooks in a linked worktree's shared Git directory.

An owning root hook starts a detached host only when its socket is missing or
refused. It reuses a compatible host and preserves incompatible or unresponsive
hosts for inspection. The host survives root exit. On Linux and Darwin, the
hook uses the packaged `nohup` and redirects every standard stream.

After a package update, shell entry warns if the running host is incompatible
or unresponsive. Inspect `concierge host status --timeout 1s` and
`.concierge/host.log`. Stop active writers and the verified old host manually
before starting a fresh root. Do not start a second host or remove state to
clear the warning. The module never restarts an older host automatically.

The [shared supervisor scheduler](heartbeats.md) requires intentional
activation. A shell refresh, state migration, or native hook registration does
not activate it. Register Codex's exact native socket and thread separately.
