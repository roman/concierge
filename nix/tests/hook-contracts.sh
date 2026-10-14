#!/usr/bin/env bash
set -euo pipefail

unset HERDR_ENV HERDR_SESSION
payload='{"hook_event_name":"SessionStart","session_id":"fixture"}'
: > "$TEST_CALLS"
output="$(TEST_PRIMARY_STATUS=held_by_other ./.agents/hooks/concierge-session-start <<< "$payload")"
[[ "$output" == *'Concierge secondary session'* ]]
if grep -E 'host|brief' "$TEST_CALLS"; then
  printf 'secondary session entered host supervision\n' >&2
  exit 1
fi

output="$(TEST_HOST_STATUS=0 TEST_RECOVERY_READY="$TMPDIR/recovered" ./.agents/hooks/concierge-session-start <<< "$payload")"
[[ "$output" == *'workers: 2 registered'* ]]
[[ "$output" == *'The primary reads projects and never changes them.'* ]]
[[ "$output" == *'Delegate coding, investigation, planning, bug reproduction, verification, and audits to workers.'* ]]

export TEST_HOST_READY="$TMPDIR/host-ready" TEST_HOST_PID="$TMPDIR/host-pids"
cleanup() {
  if [[ -f "$TEST_HOST_PID" ]]; then
    while read -r host_pid; do kill "$host_pid" 2>/dev/null || true; done < "$TEST_HOST_PID"
  fi
  if [[ -n "${TEST_WAIT_RELEASE:-}" ]]; then touch "$TEST_WAIT_RELEASE"; fi
  if [[ -n "${first_hook:-}" ]]; then kill "$first_hook" 2>/dev/null || true; fi
}
trap cleanup EXIT
# The CLI inherits HERDR_SESSION even outside Herdr; only Herdr adds a flag.
while IFS='|' read -r in_herdr session expected_session; do
  rm -f "$TEST_HOST_READY" "$TEST_HOST_PID"
  export TEST_HOST_CALLS="$TMPDIR/host-calls"
  : > "$TEST_HOST_CALLS"
  output="$(HERDR_ENV="$in_herdr" HERDR_SESSION="$session" TEST_HOST_STATUS=1 TEST_HOST_STATUS_OVERRIDE=0 ./.agents/hooks/concierge-session-start <<< "$payload")"
  [[ "$output" == *'Concierge root supervisor context'* ]]
  while IFS='|' read -r operation actual_session explicit_session; do
    [[ "$actual_session" == "$expected_session" && "$explicit_session" == "$in_herdr" ]]
  done < "$TEST_HOST_CALLS"
  grep -Fx "serve|$expected_session|$in_herdr" "$TEST_HOST_CALLS"
  cleanup
done <<'CASES'
1||default
1|work|work
0||
0|work|work
CASES
unset TEST_HOST_CALLS

payload='{"hook_event_name":"Stop","session_id":"fixture"}'
active_payload='{"hook_event_name":"Stop","session_id":"fixture","stop_hook_active":true}'
output="$(./.agents/hooks/concierge-claude-stop <<< "$active_payload" 2>&1)"
[[ -z "$output" ]]
output="$(TEST_PRIMARY_STATUS=held_by_other ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>&1)"
[[ -z "$output" ]]
for foreign_payload in \
  '{"hook_event_name":"Stop","session_id":"fixture","cursor_version":"fixture"}' \
  '{"hook_event_name":"Stop","session_id":"fixture","transcript_path":"/tmp/.pi/sessions/fixture.jsonl"}'; do
  output="$(./.agents/hooks/concierge-claude-stop <<< "$foreign_payload" 2>&1)"
  [[ -z "$output" ]]
done
status=0
TEST_INITIAL_RESULT=checkpoint ./.agents/hooks/concierge-claude-stop <<< "$active_payload" 2>new-wake || status=$?
[[ "$status" == 2 ]]
grep -F 'A Tincan message is pending' new-wake

export TEST_INITIAL_RESULT=checkpoint TEST_WAIT_LOG="$TMPDIR/waits" TEST_WAIT_RELEASE="$TMPDIR/release"
./.agents/hooks/concierge-claude-stop <<< "$payload" >first-output 2>&1 &
first_hook=$!
for attempt in {1..100}; do
  [[ ! -e "$TEST_WAIT_LOG" ]] || break
  sleep 0.05
done
[[ -f "$TEST_WAIT_LOG" ]]
output="$(timeout 2s ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>&1)"
[[ -z "$output" ]]
[[ "$(wc -l < "$TEST_WAIT_LOG")" == 1 ]]
touch "$TEST_WAIT_RELEASE"
wait "$first_hook" || [[ "$?" == 2 ]]
first_hook=
