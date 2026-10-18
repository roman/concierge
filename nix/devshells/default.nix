inputs: pkgs:
pkgs.mkShell {
  packages = [ pkgs.go_1_25 pkgs.gopls pkgs.golines pkgs.actionlint pkgs.python3 inputs.herdr.packages.${pkgs.stdenv.hostPlatform.system}.default ];
}
