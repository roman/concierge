{ conciergeFlake }:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.concierge;
  packages = conciergeFlake.packages.${pkgs.stdenv.hostPlatform.system};
  commandPath = lib.makeBinPath [
    cfg.package
    cfg.tincanPackage
    pkgs.coreutils
    pkgs.jq
    pkgs.flock
  ];
  commonHook = ''
    set -euo pipefail
    export PATH=${lib.escapeShellArg commandPath}:"$PATH"
    repository_root="''${CONCIERGE_PROJECT_ROOT:-}"
    [[ -n "$repository_root" ]] || exit 0
    repository_root="$(cd "$repository_root" && pwd -P)"
    [[ "$(pwd -P)" == "$repository_root" ]] || exit 0
    [[ -z "''${CONCIERGE_WORKER:-}" ]] || exit 0
    concierge_command=${lib.escapeShellArg "${cfg.package}/bin/concierge"}
    tincan_command=${lib.escapeShellArg "${cfg.tincanPackage}/bin/tincan"}
  '';
  hook = name: pkgs.writeShellScript name (commonHook + builtins.readFile (./hooks + "/${name}.sh"));
  skill = pkgs.writeText "concierge-local-supervisor-skill" (
    builtins.replaceStrings [ "@commandPath@" ] [ commandPath ] (builtins.readFile ./skills/SKILL.md)
  );
in
{
  options.concierge = {
    enable = lib.mkEnableOption "Concierge root supervision";
    package = lib.mkOption {
      type = lib.types.package;
      default = packages.concierge;
      description = "Concierge package used by hooks and the shell.";
    };
    tincanPackage = lib.mkOption {
      type = lib.types.package;
      default = packages.tincan;
      description = "Matching Tincan package used by hooks and the shell.";
    };
  };
  config = lib.mkIf cfg.enable {
    packages = [
      cfg.package
      cfg.tincanPackage
    ];
    files = {
      ".agents/hooks/concierge-session-start".source = hook "concierge-session-start";
      ".agents/hooks/concierge-session-end".source = hook "concierge-session-end";
      ".agents/hooks/concierge-claude-stop".source = hook "concierge-claude-stop";
      ".agents/skills/concierge-local-supervisor/SKILL.md".source = skill;
      ".claude/skills/concierge-local-supervisor/SKILL.md".source = skill;
    };
    enterShell = ''
      export CONCIERGE_PROJECT_ROOT="$(${pkgs.coreutils}/bin/realpath -m -- "''${DEVENV_ROOT:-$PWD}")"
      export CONCIERGE_HOME="''${CONCIERGE_HOME:-$CONCIERGE_PROJECT_ROOT/.concierge}"
      export TINCAN_HOME="''${TINCAN_HOME:-$CONCIERGE_PROJECT_ROOT/.tincan}"
      if [ -z "''${CONCIERGE_WORKER:-}" ]; then
        if [ ! -f "$CONCIERGE_HOME/.task-state-version" ] || [ "$(cat "$CONCIERGE_HOME/.task-state-version")" != 2 ]; then
          printf 'Concierge version 2 state is required. Run concierge state init for fresh state; inspect existing state before migration.\n' >&2
        else
          concierge_host_args=(--root "$CONCIERGE_HOME" --message-root "$TINCAN_HOME" host)
          if [ "''${HERDR_ENV:-}" = 1 ]; then
            concierge_host_args+=(--session "''${HERDR_SESSION:-default}")
          fi
          concierge_host_status=0
          ${cfg.package}/bin/concierge "''${concierge_host_args[@]}" status --timeout 1s >/dev/null 2>&1 || concierge_host_status=$?
          if [ "$concierge_host_status" -ge 2 ]; then
            printf 'Concierge host is incompatible or unresponsive. Inspect the host; a manual restart is required after a package update.\n' >&2
          fi
          unset concierge_host_args concierge_host_status
        fi
        concierge_check_hook() {
          if ! ${pkgs.jq}/bin/jq -e --arg event "$2" \
            --arg command 'if [ -z "''${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/'"$3"'; fi' \
            'any(.hooks[$event][]?.hooks[]?; .type == "command" and .command == $command and (if $event == "Stop" then .asyncRewake == true and (.timeout | type) == "number" and .timeout >= 28800 else true end))' \
            "$CONCIERGE_PROJECT_ROOT/$1" >/dev/null 2>&1; then
            printf 'Concierge hook settings need review: %s (%s)\n' "$1" "$2" >&2
          fi
        }
        concierge_check_hook .claude/settings.json SessionStart concierge-session-start
        concierge_check_hook .claude/settings.json Stop concierge-claude-stop
        concierge_check_hook .codex/hooks.json SessionStart concierge-session-start
        concierge_check_hook .codex/hooks.json SessionEnd concierge-session-end
        unset -f concierge_check_hook
      fi
    '';
  };
}
