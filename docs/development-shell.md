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
    "SessionStart": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CLAUDE_PROJECT_DIR\" && ./.agents/hooks/concierge-session-start; fi", "timeout": 15}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CLAUDE_PROJECT_DIR\" && ./.agents/hooks/concierge-claude-activity; fi", "onFailure": "block"}]}],
    "Stop": [{"hooks": [
      {"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CLAUDE_PROJECT_DIR\" && ./.agents/hooks/concierge-claude-activity; fi"},
      {"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CLAUDE_PROJECT_DIR\" && ./.agents/hooks/concierge-claude-stop; fi", "asyncRewake": true, "timeout": 28800}
    ]}]
  }
}
```

Use these definitions in `.codex/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CONCIERGE_PROJECT_ROOT\" && ./.agents/hooks/concierge-session-start; fi", "timeout": 15}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "if [ -z \"${CONCIERGE_WORKER:-}\" ]; then cd \"$CONCIERGE_PROJECT_ROOT\" && ./.agents/hooks/concierge-session-end; fi", "timeout": 3}]}]
  }
}
```

Preserve unrelated native settings when adding the definitions. Review and
approve the initial hook definitions through each harness's normal trust flow.
Claude commands change to `CLAUDE_PROJECT_DIR` before running the hook. Codex
commands use `CONCIERGE_PROJECT_ROOT`, exported by the development shell.
Both forms work from subdirectories and other repositories, including project
paths with spaces. Keep the directory change inside the worker guard.
The hook itself requires the shell's project root as its working directory.
An absolute hook path alone would find the script but skip its work.
The shell settings check reports the old guarded relative commands as needing
review. Replace them with the definitions above after updating the pin.

Codex runs hook commands in the session working directory. Its
[hook documentation](https://developers.openai.com/codex/hooks) recommends Git
root resolution for repository hooks. Git root resolution selects the wrong
repository after a session changes to another checkout. These commands use the
existing shell variable instead. Codex documents no native project-root
variable for project hooks. Start Codex from the development shell to inherit
`CONCIERGE_PROJECT_ROOT`.

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

## Automatic Codex startup

Adopt the launcher once in the existing shell module:

```nix
concierge.enable = true;
concierge.codex.enable = true;
concierge.codex.package = existingNativeCodexPackage;
```

Select the package already used for the normal Codex command. Preserve any
native packaging that supplies worktree directories or other operator policy.
The launcher requires explicit Unix listeners and `--remote` TUI attachment.
The native acceptance target is Codex 0.162.1. A missing package selection is a
configuration error. Do not configure the generated launcher as its own native
package.

This option authorizes initial scheduler activation. Each future `codex` start
creates a private listener and attaches the original TUI to it. The existing
SessionStart hook claims ownership, verifies the listener's same-user kernel
peer against the native hook process incarnation, verifies its thread and cwd,
and registers delivery. The operator supplies no socket or bootstrap prompt.
Repeated hooks preserve the binding generation. Subsequent starts preserve the
existing host clock, quiet count, and explicit operator pause. Only an explicit
`heartbeat resume` clears that pause.

Keep the committed hook definitions above unchanged. Approve project trust and
hook trust through Codex's normal controls during deployment. The module does
not grant trust or change native permissions. Enter the refreshed shell before
starting the new primary. Verify that `command -v codex` selects the generated
launcher. Run `concierge heartbeat show` after startup to inspect registration.
The launcher stops its private native server when the TUI exits. The shared
Concierge host survives and retains unacknowledged events.

Worker and noninteractive commands use the selected native package directly.
Explicit `--remote` and `--no-daemon` launches require the native package's
command; the managed entry path reports a usage error for these options.
The shared native daemon is never discovered, replaced, or restarted.

Without this option, the [shared supervisor scheduler](heartbeats.md) requires
explicit activation and explicit Codex destination registration. Shell entry,
state migration, and ordinary registration preserve the inactive default.
