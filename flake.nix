{
  description = "Concierge local task supervision";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/release-25.11";
    herdr.url = "github:herdrdev/herdr/v0.9.3";
    treehouse = {
      url = "github:kunchenguid/treehouse/v3.1.1";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    tincan = {
      url = "github:roman/tincan/4d94549baf1127db45ff39e401410f00731be2e9";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.herdr.follows = "herdr";
      inputs.flake-parts.follows = "flake-parts";
      inputs.nixDir.follows = "nixDir";
    };
    devshell-modules = {
      url = "github:roman/devshell-modules/80a5da03e15a2d2c9b43abf08bd8a9d18442429e";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixDir = {
      url = "github:roman/nixDir/v3";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
      inputs.devshell-modules.follows = "devshell-modules";
    };
  };

  outputs = inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [ inputs.nixDir.flakeModule ./nix/checks.nix ];
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
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
