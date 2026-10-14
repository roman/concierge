{
  pkgs,
  devshellModules,
  conciergeModule,
  conciergePackage,
}:
let
  evaluate = import (devshellModules + "/nix/lib") {
    inherit pkgs;
    inputs.git-hooks = devshellModules.inputs.git-hooks;
  };
  recordedConcierge = pkgs.writeShellScriptBin "concierge" ''
    case " $* " in
      *' host serve '*) printf '%s\n' "$$" >> "$TEST_HOST_PID" ;;
    esac
    exec ${conciergePackage}/bin/concierge "$@"
  '';
  result = evaluate {
    modules = [
      conciergeModule
      {
        concierge.enable = true;
        concierge.package = recordedConcierge;
      }
    ];
  };
in
pkgs.runCommand "concierge-devshell-host"
  {
    nativeBuildInputs = [
      pkgs.bash
      pkgs.coreutils
      pkgs.gnugrep
      pkgs.jq
    ];
    shellEntry = result.shellHook;
  }
  ''
    unset HERDR_ENV HERDR_SESSION CONCIERGE_WORKER TINCAN_HERDR
    export DEVENV_ROOT="$TMPDIR/project"
    mkdir -p "$DEVENV_ROOT"
    cd "$DEVENV_ROOT"
    ${conciergePackage}/bin/concierge --root "$DEVENV_ROOT/.concierge" state init
    cp ${pkgs.bash}/bin/bash "$TMPDIR/codex"
    export TEST_HOST_PID="$TMPDIR/host.pid"
    host_pid=
    cleanup() {
      if [ -f "$TEST_HOST_PID" ]; then
        while read -r host_pid; do kill "$host_pid" 2>/dev/null || true; done < "$TEST_HOST_PID"
      fi
    }
    trap cleanup EXIT
    "$TMPDIR/codex" -e <<'ROOT'
    eval "$shellEntry"
    jq -n --arg cwd "$PWD" '{cwd:$cwd,session_id:"module-root",hook_event_name:"SessionStart"}' > payload
    ./.agents/hooks/concierge-session-start < payload > start-output
    grep -F 'Concierge root supervisor context' start-output
    grep -F 'The primary reads projects and never changes them.' start-output
    grep -F 'Delegate coding, investigation, planning, bug reproduction, verification, and audits to workers.' start-output
    ${conciergePackage}/bin/concierge project show fixture > default-project
    grep -qx 'merge: off' default-project
    grep -qx 'delivery: pr' default-project
    mkdir -p "$CONCIERGE_HOME/config"
    printf '%s\n' '{"fixture":{"merge":"on","delivery":"fast-forward"}}' > "$CONCIERGE_HOME/config/projects.json"
    ${conciergePackage}/bin/concierge project show fixture > configured-project
    grep -qx 'merge: on' configured-project
    grep -qx 'delivery: fast-forward' configured-project
    ${conciergePackage}/bin/concierge task report --help > report-help
    grep -F 'Reply message ID' report-help
    ${conciergePackage}/bin/concierge host status > first-status
    ./.agents/hooks/concierge-session-start < payload > second-output
    ${conciergePackage}/bin/concierge host status > second-status
    cmp first-status second-status
    jq -n --arg cwd "$PWD" '{cwd:$cwd,session_id:"module-root",hook_event_name:"SessionEnd"}' | ./.agents/hooks/concierge-session-end
    test ! -e "$CONCIERGE_HOME/primary.json"
    ROOT
    # Root exit must leave exactly the recorded host available.
    ${conciergePackage}/bin/concierge --root "$DEVENV_ROOT/.concierge" host status > departed-status
    test "$(wc -l < "$TEST_HOST_PID")" -eq 1
    host_pid=$(cat "$TEST_HOST_PID")
    kill -0 "$host_pid"
    test "$(stat -c %a "$DEVENV_ROOT/.concierge/host.log")" = 600
    kill "$host_pid"
    host_pid=
    touch "$out"
  ''
