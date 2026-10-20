#!/usr/bin/env bash
set -euo pipefail
unset CONCIERGE_WORKER

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export TEST_NATIVE_CALLS="$work/calls"
export TEST_NATIVE_SERVER="$work/server"
printf '#!%s\n' "$(command -v bash)" > "$work/native"
cat >> "$work/native" <<'NATIVE'
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_NATIVE_CALLS"
if [[ "${1:-}" == app-server && "$#" -ge 3 ]]; then
  printf '%s\n' "$$" > "$TEST_NATIVE_SERVER"
  exec python3 -c 'import socket,sys,time; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1]); s.listen(); time.sleep(60)' "${3#unix://}"
fi
if [[ "${1:-}" == --remote ]]; then
  [[ -S "${2#unix://}" ]]
  [[ "$CONCIERGE_CODEX_SOCKET" == "${2#unix://}" ]]
  printf '%s\n' "$CONCIERGE_CODEX_SOCKET" > "$TEST_NATIVE_CALLS.socket"
  exit 7
fi
NATIVE
chmod +x "$work/native"
export native_codex="$work/native"
launcher="${1:?launcher source required}"

status=0
bash "$launcher" 'hello world' || status=$?
[[ "$status" == 7 ]]
test "$(wc -l < "$TEST_NATIVE_CALLS")" -eq 2
grep -F -- '--remote unix://' "$TEST_NATIVE_CALLS"
test ! -e "$(cat "$TEST_NATIVE_CALLS.socket")"
if kill -0 "$(cat "$TEST_NATIVE_SERVER")" 2>/dev/null; then
  printf 'launcher left the native server running\n' >&2
  exit 1
fi
for mode in '--version' 'exec' 'app-server' 'queue'; do
  : > "$TEST_NATIVE_CALLS"
  bash "$launcher" "$mode"
  test "$(wc -l < "$TEST_NATIVE_CALLS")" -eq 1
  grep -Fx -- "$mode" "$TEST_NATIVE_CALLS"
done
: > "$TEST_NATIVE_CALLS"
CONCIERGE_WORKER=fixture bash "$launcher" resume worker-thread
grep -Fx 'resume worker-thread' "$TEST_NATIVE_CALLS"
test "$(wc -l < "$TEST_NATIVE_CALLS")" -eq 1

: > "$TEST_NATIVE_CALLS"
status=0
bash "$launcher" -c 'features.hooks=true' -- queue || status=$?
[[ "$status" == 7 ]]
test "$(wc -l < "$TEST_NATIVE_CALLS")" -eq 2
grep -E '^app-server .* -c features.hooks=true$' "$TEST_NATIVE_CALLS"
