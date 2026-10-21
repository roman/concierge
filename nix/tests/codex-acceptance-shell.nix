# Build a disposable shell that exercises the production module and launcher.
{ nativePackage, projectRoot }:
let
  flake = builtins.getFlake (toString ../..);
  pkgs = import flake.inputs.nixpkgs { system = builtins.currentSystem; };
  lib = pkgs.lib;
  evaluate = import (flake.inputs.devshell-modules + "/nix/lib") {
    inherit pkgs;
    inputs.git-hooks = flake.inputs.devshell-modules.inputs.git-hooks;
  };
  result = evaluate {
    modules = [
      flake.devShellModules.default
      {
        concierge.enable = true;
        concierge.codex.enable = true;
        concierge.codex.package = builtins.storePath nativePackage;
      }
    ];
  };
  hooks = pkgs.writeText "concierge-acceptance-hooks.json" (builtins.toJSON {
    hooks = {
      SessionStart = [{ hooks = [{ type = "command"; command = ''if [ -z "''${CONCIERGE_WORKER:-}" ]; then cd "$CONCIERGE_PROJECT_ROOT" && ./.agents/hooks/concierge-session-start; fi''; timeout = 15; }]; }];
      SessionEnd = [{ hooks = [{ type = "command"; command = ''if [ -z "''${CONCIERGE_WORKER:-}" ]; then cd "$CONCIERGE_PROJECT_ROOT" && ./.agents/hooks/concierge-session-end; fi''; timeout = 3; }]; }];
    };
  });
in
assert lib.hasPrefix "/tmp/concierge-codex-acceptance." projectRoot;
pkgs.writeShellScript "concierge-codex-acceptance" ''
  set -euo pipefail
  unset CONCIERGE_WORKER CONCIERGE_SPAWN_GENERATION CODEX_THREAD_ID CODEX_SESSION_ID
  unset HERDR_ENV HERDR_SESSION CONCIERGE_HOME TINCAN_HOME
  export DEVENV_ROOT=${lib.escapeShellArg projectRoot}
  mkdir -p "$DEVENV_ROOT/.codex"
  cd "$DEVENV_ROOT"
  export PATH=${lib.makeBinPath result.config.packages}:"$PATH"
  if [ ! -e .codex/hooks.json ]; then
    cp ${hooks} .codex/hooks.json
  fi
  if [ ! -e .concierge/.task-state-version ]; then
    ${flake.packages.${pkgs.stdenv.hostPlatform.system}.concierge}/bin/concierge --root "$DEVENV_ROOT/.concierge" state init
  fi
  ${result.shellHook}
  exec codex --no-alt-screen "$@"
''
