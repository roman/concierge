inputs: { pkgs, ... }:
let
  command = pkgs.callPackage (import ./concierge-command.nix inputs) { };
  system = pkgs.stdenv.hostPlatform.system;
  processTools = import ../process-tools.nix { inherit pkgs; };
  runtime = pkgs.symlinkJoin {
    name = "concierge-runtime";
    paths = [
      inputs.herdr.packages.${system}.default
      inputs.tincan.packages.${system}.tincan
      inputs.treehouse.packages.${system}.default
      processTools pkgs.git pkgs.gh
    ];
  };
in
pkgs.runCommand "concierge-${command.version}" {
  nativeBuildInputs = [ pkgs.makeWrapper ];
} ''
  install -Dm755 ${command}/bin/concierge "$out/bin/concierge"
  wrapProgram "$out/bin/concierge" --prefix PATH : ${runtime}/bin
''
