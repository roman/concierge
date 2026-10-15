set -euo pipefail

unset CONCIERGE_HOME TINCAN_HOME TINCAN_HERDR HERDR_SESSION HERDR_SOCKET_PATH HERDR_WORKSPACE_ID HERDR_TAB_ID HERDR_PANE_ID
export PATH=
test -x "$concierge/bin/concierge"
test "$("$concierge/bin/concierge" --version)" = "$version"
set -- "$concierge"/bin/*
test "$#" -eq 1
test "$1" = "$concierge/bin/concierge"
set -- "$tincan"/bin/*
test "$#" -eq 1
test "$1" = "$tincan/bin/tincan"
"$concierge/bin/concierge" --root "$TMPDIR/state" state init > initialized
"$grep" -qx 'version: 2' initialized
"$coreutils/bin/cp" "$bash" "$TMPDIR/codex"
"$TMPDIR/codex" -e <<'INTAKE'
"$jq" -n --arg cwd "$TMPDIR" \
  '{cwd:$cwd,session_id:"packaged-intake",hook_event_name:"SessionStart"}' | \
  "$concierge/bin/concierge" --root "$TMPDIR/state" primary claim >/dev/null
"$concierge/bin/concierge" --root "$TMPDIR/state" host serve > "$TMPDIR/host-output" &
host_pid=$!
trap 'kill "$host_pid" 2>/dev/null || true; wait "$host_pid" 2>/dev/null || true' EXIT
for attempt in {1..100}; do
  test ! -S "$TMPDIR/state/host.sock" || break
  kill -0 "$host_pid"
  "$coreutils/bin/sleep" 0.01
done
"$concierge/bin/concierge" --root "$TMPDIR/state" backlog add smoke-a1 'Packaged intake' \
  --kind task --repo exmachina >/dev/null
INTAKE
"$concierge/bin/concierge" --root "$TMPDIR/state" backlog list > backlog
"$grep" -Fx '  "smoke-a1",queued,"task","exmachina","Packaged intake"' backlog
"$concierge/bin/concierge" --root "$TMPDIR/state" backlog show smoke-a1 --full > detail
"$grep" -Fx '  title: "Packaged intake"' detail
"$tincan/bin/tincan" --root "$TMPDIR/messages" inbox send concierge --key wake --body 'Packaged wake' >/dev/null
"$concierge/bin/concierge" --root "$TMPDIR/state" --message-root "$TMPDIR/messages" > brief
"$grep" -Fx 'wakes: 1 pending for concierge' brief
"$grep" -Fx 'backlog: 1 queued' brief
"$concierge/bin/concierge" task reply --help >/dev/null
"$concierge/bin/concierge" primary --help >/dev/null
"$concierge/bin/concierge" control --help >/dev/null
"$concierge/bin/concierge" supervise --help >/dev/null

export SMOKE_WORKTREE="$TMPDIR"
# Health checks exclude PID 1, which Nix uses for the builder shell.
"$TMPDIR/codex" -e <<'HEALTH'
export SMOKE_SHELL_PID=$$
(exec -a codex "$coreutils/bin/coreutils" --coreutils-prog=sleep 60) &
agent_pid=$!
export SMOKE_FOREGROUND='[]'
if [ "$darwin" = 1 ]; then
  # Darwin's Nix sandbox blocks /bin/ps; native acceptance covers it.
  SMOKE_FOREGROUND="$(printf '[{"pid":%s,"name":"codex"}]' "$agent_pid")"
fi
trap 'kill "${host_pid:-}" "$agent_pid" 2>/dev/null || true; wait "${host_pid:-}" "$agent_pid" 2>/dev/null || true' EXIT
"$jq" --arg worktree "$TMPDIR" \
  '.state = "in_flight" | .worker = {name:"smoke",harness:"codex",session:"smoke",workspace:"w1",tab:"t1",pane:"p1",project:$worktree,worktree:$worktree,generation:"gen-smoke",agent_args:["resume","smoke"]}' \
  "$TMPDIR/state/records/smoke-a1.json" > record
"$coreutils/bin/mv" record "$TMPDIR/state/records/smoke-a1.json"
"$jq" -n --arg cwd "$TMPDIR" \
  '{cwd:$cwd,session_id:"packaged-health",hook_event_name:"SessionStart"}' | \
  "$concierge/bin/concierge" --root "$TMPDIR/state" primary claim >/dev/null
"$concierge/bin/concierge" --root "$TMPDIR/state" --message-root "$TMPDIR/messages" \
  host --herdr "$healthHerdr" serve > "$TMPDIR/health-host-output" &
host_pid=$!
for attempt in {1..100}; do
  if "$grep" -q '^status: ready$' "$TMPDIR/health-host-output"; then break; fi
  kill -0 "$host_pid"
  "$coreutils/bin/sleep" 0.01
done
"$grep" -q '^status: ready$' "$TMPDIR/health-host-output"
"$concierge/bin/concierge" --root "$TMPDIR/state" --message-root "$TMPDIR/messages" \
  supervise --herdr "$healthHerdr" task smoke-a1 > supervision
"$grep" -qx 'supervision: active' supervision
"$concierge/bin/concierge" --root "$TMPDIR/state" supervise stop task smoke-a1 > stopped
"$grep" -qx 'supervision: stopped' stopped
HEALTH
: > "$out"
