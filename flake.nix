{
  description = "Concierge local task supervision";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/release-25.11";
    herdr.url = "github:herdrdev/herdr/v0.9.5";
    treehouse = {
      url = "github:kunchenguid/treehouse/v3.1.4";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    tincan = {
      url = "github:roman/tincan/497b0d8dd6ebabe1d85f8047d652fdad7e36541a";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.herdr.follows = "herdr";
      inputs.flake-parts.follows = "flake-parts";
      inputs.nixDir.follows = "nixDir";
    };
    devshell-modules = {
      url = "github:roman/devshell-modules/320157389046a0f96712018fc57ae30be0200e40";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixDir = {
      url = "github:roman/nixDir/v3.4.0";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
      inputs.devshell-modules.follows = "devshell-modules";
    };
  };

  outputs = inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [ inputs.nixDir.flakeModule ./nix/checks.nix ];
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      nixDir = {
        enable = true;
        root = builtins.toString ./.;
        importWithInputs = true;
      };
      flake.devShellModules.default = inputs.self.devenvModules.default;
      perSystem = { self', ... }: {
        packages.default = self'.packages.concierge;
      };
    };
}
