inputs: { pkgs, ... }:
inputs.tincan.packages.${pkgs.stdenv.hostPlatform.system}.tincan
