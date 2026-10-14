{
  pkgs,
  devshellModules,
  conciergeModule,
}:
let
  evaluate = import (devshellModules + "/nix/lib") {
    inherit pkgs;
    inputs.git-hooks = devshellModules.inputs.git-hooks;
  };
  fakeConcierge = pkgs.writeShellScriptBin "concierge" ''
    printf '%s\n' "$*" >> "$TEST_CALLS"
    case "$*" in
      *'host status'*) printf 'status: incompatible\n'; exit 2 ;;
      *'primary claim'*|*'primary verify'*) cat >/dev/null; printf 'status: owner\nkind: test\n' ;;
      *'primary release'*) cat >/dev/null ;;
      *'brief'*) printf 'wakes: 0 pending for concierge\n' ;;
      *) printf 'Unexpected command\n' >&2; exit 91 ;;
    esac
  '';
  fakeTincan = pkgs.writeShellScriptBin "tincan" ''printf 'status: pending\nmessages: 1\n' '';
  result = evaluate {
    modules = [
      conciergeModule
      {
        concierge.enable = true;
        concierge.package = fakeConcierge;
        concierge.tincanPackage = fakeTincan;
      }
    ];
  };
in
pkgs.runCommand "concierge-devshell"
  {
    nativeBuildInputs = [
      pkgs.bash
      pkgs.coreutils
      pkgs.gnugrep
      pkgs.gnused
    ];
    shellEntry = result.shellHook;
  }
  ''
    export TEST_CALLS="$TMPDIR/calls"
    touch "$TEST_CALLS"
    mkdir -p "$TMPDIR/project/subdirectory"
    export DEVENV_ROOT="$TMPDIR/project"
    cd "$DEVENV_ROOT/subdirectory"
    run_entry() { eval "$shellEntry"; }
    run_entry 2>missing-warning
    grep -F 'concierge state init' missing-warning
    test ! -e "$DEVENV_ROOT/.concierge"
    test ! -e "$DEVENV_ROOT/.tincan"
    test ! -s "$TEST_CALLS"
    test "$CONCIERGE_HOME" = "$DEVENV_ROOT/.concierge"
    test "$TINCAN_HOME" = "$DEVENV_ROOT/.tincan"
    for hook in concierge-session-start concierge-session-end concierge-claude-stop; do
      test -x "$DEVENV_ROOT/.agents/hooks/$hook"
    done
    test -r "$DEVENV_ROOT/.agents/skills/concierge-local-supervisor/SKILL.md"
    test -r "$DEVENV_ROOT/.claude/skills/concierge-local-supervisor/SKILL.md"
    test ! -e "$DEVENV_ROOT/.claude/settings.json"
    test ! -e "$DEVENV_ROOT/.codex/hooks.json"
    # A root hook must also preserve explicit initialization before shell re-entry.
    (cd "$DEVENV_ROOT" && printf '{}' | ./.agents/hooks/concierge-session-start) >missing-start
    grep -F 'concierge state init' missing-start
    test ! -e "$CONCIERGE_HOME"
    test ! -s "$TEST_CALLS"
    mkdir -p "$CONCIERGE_HOME"
    run_entry 2>uninitialized-warning
    grep -F 'concierge state init' uninitialized-warning
    (cd "$DEVENV_ROOT" && printf '{}' | ./.agents/hooks/concierge-session-start) >uninitialized-start
    grep -F 'concierge state init' uninitialized-start
    test ! -s "$TEST_CALLS"
    printf '1\n' > "$CONCIERGE_HOME/.task-state-version"
    run_entry 2>legacy-state-warning
    grep -F 'concierge state init' legacy-state-warning
    (cd "$DEVENV_ROOT" && printf '{}' | ./.agents/hooks/concierge-session-start) >legacy-state-start
    grep -F 'concierge state init' legacy-state-start
    test ! -s "$TEST_CALLS"
    printf '2\n' > "$CONCIERGE_HOME/.task-state-version"
    run_entry 2>older-warning
    grep -F 'manual restart' older-warning
    if grep -E 'state init|serve' "$TEST_CALLS"; then exit 1; fi
    : > "$TEST_CALLS"
    # A hook entered from another directory or a worker cannot claim the root.
    "$DEVENV_ROOT/.agents/hooks/concierge-session-start" </dev/null
    cd "$DEVENV_ROOT"
    CONCIERGE_WORKER=fixture ./.agents/hooks/concierge-session-start </dev/null
    test ! -s "$TEST_CALLS"
    # An incompatible host blocks the brief and is never replaced.
    printf '{}' | ./.agents/hooks/concierge-session-start > blocked-start
    grep -F 'manual restart' blocked-start
    if grep -E 'serve|brief' "$TEST_CALLS"; then exit 1; fi
    # Existing SessionStart settings cannot hide missing Stop or SessionEnd hooks.
    printf '%s\n' '{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"./.agents/hooks/concierge-session-start"}]}]}}' > .claude/settings.json
    mkdir -p .codex
    cp .claude/settings.json .codex/hooks.json
    run_entry 2>incomplete-settings
    grep -F '.claude/settings.json' incomplete-settings
    grep -F '.codex/hooks.json' incomplete-settings
    printf '%s\n' '{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"./.agents/hooks/concierge-session-start"}]}],"Stop":[{"hooks":[{"type":"command","command":"./.agents/hooks/concierge-claude-stop","timeout":28800}]}]}}' > .claude/settings.json
    run_entry 2>wrong-stop-options
    grep -F '.claude/settings.json (Stop)' wrong-stop-options
    sed 's/"timeout":28800/"asyncRewake":true,"timeout":15/' .claude/settings.json > short-stop.json
    cp short-stop.json .claude/settings.json
    run_entry 2>short-stop-warning
    grep -F '.claude/settings.json (Stop)' short-stop-warning
    stop_status=0
    printf '%s\n' '{"hook_event_name":"Stop","session_id":"fixture"}' | ./.agents/hooks/concierge-claude-stop 2>stop-wake || stop_status=$?
    test "$stop_status" = 2 || { cat stop-wake; printf 'Stop exit: %s\n' "$stop_status"; exit 1; }
    test -f "$CONCIERGE_HOME/claude-stop.lock"
    grep -F 'A Tincan message is pending' stop-wake
    touch "$out"
  ''
