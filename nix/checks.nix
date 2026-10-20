{ inputs, ... }:
{
  perSystem = { pkgs, self', ... }:
    let
      golinesFlags = "-m 100 --no-reformat-tags --base-formatter=gofmt";
    in {
      formatter = pkgs.writeShellApplication {
        name = "concierge-format";
        runtimeInputs = [ pkgs.golines pkgs.go_1_25 ];
        text = ''
          if [ "$#" -eq 0 ]; then
            set -- .
          fi
          exec golines ${golinesFlags} -w "$@"
        '';
      };
      checks = {
        codex-launch = pkgs.runCommand "concierge-codex-launch" {
          nativeBuildInputs = [ pkgs.bash pkgs.coreutils pkgs.gnugrep pkgs.python3 ];
        } ''
          bash ${./tests/codex-launch.sh} ${./hooks/concierge-codex.sh}
          touch "$out"
        '';
        inherit (self'.packages) concierge;
        native-hook-python = pkgs.runCommand "concierge-native-hook-python" {
          nativeBuildInputs = self'.devShells.default.nativeBuildInputs;
        } ''
          python3 -c 'import json, socket, tempfile; socket.socket(socket.AF_UNIX).close()'
          touch "$out"
        '';
        devshell = import ./tests/devshell.nix {
          inherit pkgs;
          devshellModules = inputs.devshell-modules;
          conciergeModule = inputs.self.devenvModules.default;
        };
        devshell-host = import ./tests/devshell-host.nix {
          inherit pkgs;
          devshellModules = inputs.devshell-modules;
          conciergeModule = inputs.self.devShellModules.default;
          conciergePackage = self'.packages.concierge;
        };
        concierge-smoke = pkgs.runCommand "concierge-smoke" {
          concierge = self'.packages.concierge;
          tincan = self'.packages.tincan;
          version = self'.packages.concierge-command.version;
          healthHerdr = pkgs.writeShellScript "concierge-health-herdr" (builtins.readFile ./tests/health-herdr.sh);
          coreutils = pkgs.coreutils;
          grep = "${pkgs.gnugrep}/bin/grep";
          jq = "${pkgs.jq}/bin/jq";
          bash = "${pkgs.bash}/bin/bash";
          darwin = if pkgs.stdenv.isDarwin then "1" else "0";
        } "bash ${./tests/smoke.sh}";
        go-format = pkgs.runCommand "concierge-go-format" {
          nativeBuildInputs = [ pkgs.golines pkgs.go_1_25 ];
          src = import ./source.nix { inherit (pkgs) lib; };
        } ''
          cd "$src"
          unformatted="$(golines ${golinesFlags} -l .)"
          if [ -n "$unformatted" ]; then
            printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
            exit 1
          fi
          touch "$out"
        '';
      };
    };
}
