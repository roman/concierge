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
    if [ "$3" = heartbeat ] || { [ "$3" = --message-root ] && [ "$5" = heartbeat ]; }; then
      cat >/dev/null
      case "$*" in
        *"heartbeat activity") exit "''${TEST_ACTIVITY_STATUS:-0}" ;;
        *"heartbeat wait")
          if [ "''${TEST_CONSUMER_STATUS:-2}" = 2 ]; then
            printf 'Concierge supervisor event fixture. Run the verified heartbeat claim command.\n' >&2
          fi
          exit "''${TEST_CONSUMER_STATUS:-2}"
          ;;
        *) exit 91 ;;
      esac
    fi
    case "$3" in
      operator)
        printf 'status: %s\nopen_items: 0\n' "''${TEST_REMINDER_STATUS:-quiet}"
        if [ "''${TEST_REMINDER_STATUS:-quiet}" = due ]; then
          printf 'Waiting on you: approve the stop. Use PushNotification.\n'
        fi
        ;;
      primary)
        cat >/dev/null
        printf 'status: %s\nkind: test\n' "''${TEST_PRIMARY_STATUS:-owner}"
        test "''${TEST_PRIMARY_STATUS:-owner}" = owner
        ;;
      --message-root)
        if [ "$5" = brief ]; then
          if [ -n "''${TEST_RECOVERY_READY:-}" ]; then test -f "$TEST_RECOVERY_READY" || exit 91; fi
          printf 'workers: 2 registered\n'
          exit 0
        fi
        test "$5" = host
        host_operation="$6"
        host_session="''${HERDR_SESSION:-}"
        explicit_session=0
        if [ "$6" = --session ]; then
          explicit_session=1
          host_session="$7"
          host_operation="$8"
        fi
        if [ -n "''${TEST_HOST_CALLS:-}" ]; then
          printf '%s|%s|%s\n' "$host_operation" "$host_session" "$explicit_session" >> "$TEST_HOST_CALLS"
        fi
        case "$host_operation" in
          status)
            if [ -n "''${TEST_RECOVERY_READY:-}" ]; then touch "$TEST_RECOVERY_READY"; fi
            if [ "''${TEST_HOST_STATUS:-2}" = 1 ] && [ ! -f "$TEST_HOST_READY" ]; then
              printf 'status: unavailable\n'; exit 1
            fi
            printf 'status: fixture\n'
            printf 'host_revision: "%s"\nbinary_revision: "revision-b"\nupgrade_required: %s\n' "''${TEST_HOST_REVISION:-revision-b}" "''${TEST_UPGRADE_REQUIRED:-no}"
            exit "''${TEST_HOST_STATUS_OVERRIDE:-''${TEST_HOST_STATUS:-2}}"
            ;;
          serve)
            printf '%s\n' "$$" >> "$TEST_HOST_PID"
            touch "$TEST_HOST_READY"
            exec sleep 60
            ;;
        esac
        ;;
      *) exit 91 ;;
    esac
  '';
  fakeTincan = pkgs.writeShellScriptBin "tincan" ''
    result="''${TEST_INITIAL_RESULT:-pending}"
    if [ "$7" != 1ms ]; then
      result=pending
      if [ -n "''${TEST_WAIT_LOG:-}" ]; then printf 'wait\n' >> "$TEST_WAIT_LOG"; fi
      if [ -n "''${TEST_WAIT_RELEASE:-}" ]; then
        until [ -e "$TEST_WAIT_RELEASE" ]; do sleep 0.01; done
      fi
    fi
    printf 'status: %s\ncount: 1\n' "$result"
  '';
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
    nativeBuildInputs = result.config.packages ++ [
      pkgs.bash
      pkgs.coreutils
      pkgs.gnugrep
      pkgs.gnused
      pkgs.git
      pkgs.diffutils
    ];
    shellEntry = result.shellHook;
  }
  ''
    python3 -c 'import json, socket, tempfile; socket.socket(socket.AF_UNIX).close()'
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
    for hook in concierge-session-start concierge-session-end concierge-claude-stop concierge-claude-activity; do
      test -x "$DEVENV_ROOT/.agents/hooks/$hook"
    done
    test -r "$DEVENV_ROOT/.agents/skills/concierge-local-supervisor/SKILL.md"
    test -r "$DEVENV_ROOT/.claude/skills/concierge-local-supervisor/SKILL.md"
    cmp "$DEVENV_ROOT/.agents/skills/concierge-local-supervisor/SKILL.md" "$DEVENV_ROOT/.claude/skills/concierge-local-supervisor/SKILL.md"
    if grep -F 'Exmachina' "$DEVENV_ROOT/.agents/skills/concierge-local-supervisor/SKILL.md"; then exit 1; fi
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
    printf '3\n' > "$CONCIERGE_HOME/.task-state-version"
    run_entry 2>remote-state-warning
    if ! grep -F 'manual restart' remote-state-warning; then
      printf 'format-three shell entry did not inspect the host\n' >&2
      cat remote-state-warning >&2
      exit 1
    fi
    printf '2\n' > "$CONCIERGE_HOME/.task-state-version"
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
    grep -F 'Concierge supervisor event fixture' stop-wake
    start_command='if [ -z "''${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/concierge-session-start; fi'
    stop_command='if [ -z "''${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/concierge-claude-stop; fi'
    end_command='if [ -z "''${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/concierge-session-end; fi'
    activity_command='if [ -z "''${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/concierge-claude-activity; fi'
    ${pkgs.jq}/bin/jq -n --arg start "$start_command" --arg stop "$stop_command" --arg end "$end_command" --arg activity "$activity_command" \
      '{hooks:{SessionStart:[{hooks:[{type:"command",command:$start}]}],UserPromptSubmit:[{hooks:[{type:"command",command:$activity,onFailure:"block"}]}],Stop:[{hooks:[{type:"command",command:$activity},{type:"command",command:$stop,asyncRewake:true,timeout:28800}]}],SessionEnd:[{hooks:[{type:"command",command:$end}]}]}}' \
      > .claude/settings.json
    cp .claude/settings.json .codex/hooks.json
    run_entry 2>guarded-settings-warning
    if grep -F 'hook settings need review' guarded-settings-warning; then exit 1; fi
    ${pkgs.jq}/bin/jq \
      '.hooks.SessionStart[0].hooks[0].command = "./.agents/hooks/concierge-session-start" |
       .hooks.Stop[0].hooks[0].command = "./.agents/hooks/concierge-claude-stop" |
       .hooks.SessionEnd[0].hooks[0].command = "./.agents/hooks/concierge-session-end"' \
      .claude/settings.json > unguarded-settings.json
    cp unguarded-settings.json .claude/settings.json
    cp unguarded-settings.json .codex/hooks.json
    run_entry 2>unguarded-settings-warning
    grep -F '.claude/settings.json (SessionStart)' unguarded-settings-warning
    grep -F '.claude/settings.json (Stop)' unguarded-settings-warning
    grep -F '.codex/hooks.json (SessionStart)' unguarded-settings-warning
    grep -F '.codex/hooks.json (SessionEnd)' unguarded-settings-warning
    ${pkgs.bash}/bin/bash ${./hook-contracts.sh}
    export CONCIERGE_HOME="$TMPDIR/shared-state"
    export TINCAN_HOME="$TMPDIR/shared-messages"
    export CONCIERGE_WORKER=fixture
    : > "$TEST_CALLS"
    run_entry 2>worker-warning
    test "$CONCIERGE_HOME" = "$TMPDIR/shared-state"
    test "$TINCAN_HOME" = "$TMPDIR/shared-messages"
    test ! -s worker-warning
    test ! -s "$TEST_CALLS"
    unset CONCIERGE_WORKER
    mkdir -p "$CONCIERGE_HOME"
    printf '2\n' > "$CONCIERGE_HOME/.task-state-version"
    run_entry 2>explicit-root-warning
    test "$CONCIERGE_HOME" = "$TMPDIR/shared-state"
    test "$TINCAN_HOME" = "$TMPDIR/shared-messages"
    if grep -F 'version 2 state is required' explicit-root-warning; then exit 1; fi
    export GIT_CONFIG_GLOBAL=/dev/null
    export GIT_CONFIG_NOSYSTEM=1
    git -c init.templateDir= init -b main "$TMPDIR/main-checkout"
    git -C "$TMPDIR/main-checkout" -c user.name=Fixture -c user.email=fixture@example.invalid commit --allow-empty -m Initial
    mkdir -p "$TMPDIR/main-checkout/.git/hooks"
    printf 'fixture\n' > "$TMPDIR/main-checkout/.git/hooks/pre-commit"
    cp -r "$TMPDIR/main-checkout/.git/hooks" "$TMPDIR/hooks-before"
    git -C "$TMPDIR/main-checkout" worktree add --detach "$TMPDIR/linked-checkout"
    export DEVENV_ROOT="$TMPDIR/linked-checkout"
    export CONCIERGE_WORKER=fixture
    cd "$DEVENV_ROOT"
    run_entry 2>linked-worker-warning
    test ! -s linked-worker-warning
    diff -r "$TMPDIR/hooks-before" "$TMPDIR/main-checkout/.git/hooks"
    touch "$out"
  ''
