inputs: { pkgs, ... }:
pkgs.buildGoModule {
  pname = "concierge-command";
  version = pkgs.lib.trim (builtins.readFile ../../VERSION);
  src = import ../source.nix { inherit (pkgs) lib; };
  go = pkgs.go_1_25;
  vendorHash = "sha256-mQRUfWVqs5Z0pxusssZVBj2ArsRalPU/4PvTP0oBX4k=";
  subPackages = [ "cmd/concierge" "cmd/concierge-usage" ];
  ldflags = [
    "-X github.com/roman/concierge/pkg/conciergecli.version=${pkgs.lib.trim (builtins.readFile ../../VERSION)}"
    "-X github.com/roman/concierge/pkg/sessionusagecli.version=${pkgs.lib.trim (builtins.readFile ../../VERSION)}"
    "-X github.com/roman/concierge/pkg/conciergecli.revision=${inputs.self.rev or inputs.self.dirtyRev or "unknown"}"
  ];
  nativeCheckInputs = [
    pkgs.git pkgs.openssh
    inputs.treehouse.packages.${pkgs.stdenv.hostPlatform.system}.default
    (import ../process-tools.nix { inherit pkgs; })
  ];
  TREEHOUSE_TEST_EXECUTABLE = "${inputs.treehouse.packages.${pkgs.stdenv.hostPlatform.system}.default}/bin/treehouse";
  TINCAN_TEST_EXECUTABLE = "${inputs.tincan.packages.${pkgs.stdenv.hostPlatform.system}.tincan}/bin/tincan";
  transportRevision = builtins.substring 0 12 inputs.tincan.sourceInfo.rev;
  darwin = if pkgs.stdenv.isDarwin then "1" else "0";
  checkPhase = ''
    runHook preCheck
    bash ${../tests/package-boundary.sh}
    runHook postCheck
  '';
}
