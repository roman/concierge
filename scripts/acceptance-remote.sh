#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" != 2 || -z "$1" || "$2" != /* ]]; then
  printf 'Usage: bash scripts/acceptance-remote.sh SAVED_MACHINE /absolute/path/report.md\n' >&2
  printf 'Run from a native Codex or Claude session with a loaded SSH agent.\n' >&2
  exit 2
fi

if [[ -e "$2" || -L "$2" ]]; then
  printf 'The report path already exists. Select a new path.\n' >&2
  exit 2
fi

source_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [[ -n "$(git -C "$source_directory" status --porcelain --untracked-files=all --ignored)" ]]; then
  printf 'Commit changes and remove ignored files before acceptance.\n' >&2
  exit 1
fi

package="$(nix build "$source_directory#concierge" --no-link --print-out-paths)"
export CONCIERGE_ACCEPTANCE_BIN="$package/bin/concierge"
export CONCIERGE_ACCEPTANCE_SOURCE="$source_directory"
export CONCIERGE_ACCEPTANCE_MACHINE="$1"
export CONCIERGE_ACCEPTANCE_REPORT="$2"
export CONCIERGE_ACCEPTANCE_REMOTE=1
cd "$source_directory"
exec nix develop -c go test ./integration -run '^TestSavedMachineAcceptance$' -v -timeout 70m
