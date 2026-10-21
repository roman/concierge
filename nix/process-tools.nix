{ pkgs }:
if pkgs.stdenv.isLinux then pkgs.procps else pkgs.darwin.ps
