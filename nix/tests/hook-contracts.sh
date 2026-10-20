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
[[ "$output" == *'The primary reads projects and delegates project changes.'* ]]
[[ "$output" == *'The operator may authorize a specific operation or a narrowly scoped integration responsibility.'* ]]
[[ "$output" == *'Keep every exception within its stated scope.'* ]]
[[ "$output" == *'Delegate coding, investigation, planning, bug reproduction, verification, and audits to workers.'* ]]
[[ "$output" == *'Concierge revisions: host revision-b, binary revision-b.'* ]]

printf '3\n' > "$DEVENV_ROOT/.concierge/.task-state-version"
output="$(TEST_HOST_STATUS=0 ./.agents/hooks/concierge-session-start <<< "$payload")"
if [[ "$output" != *'Concierge root supervisor context'* ]]; then
  printf 'format-three startup did not reach the primary host: %s\n' "$output" >&2
  exit 1
fi
for unsupported in 1 4 invalid; do
  printf '%s\n' "$unsupported" > "$DEVENV_ROOT/.concierge/.task-state-version"
  : > "$TEST_CALLS"
  output="$(TEST_HOST_STATUS=0 ./.agents/hooks/concierge-session-start <<< "$payload")"
  [[ "$output" == *'concierge state init'* ]]
  [[ ! -s "$TEST_CALLS" ]]
done
printf '2\n' > "$DEVENV_ROOT/.concierge/.task-state-version"

output="$(TEST_HOST_STATUS=0 TEST_HOST_REVISION=revision-a TEST_UPGRADE_REQUIRED=yes ./.agents/hooks/concierge-session-start <<< "$payload")"
[[ "$output" == *'Concierge revisions: host revision-a, binary revision-b.'* ]]
[[ "$output" == *"concierge --root $DEVENV_ROOT/.concierge --message-root $DEVENV_ROOT/.tincan host restart"* ]]
[[ "$output" == *'Start a new primary session after the restart.'* ]]

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
status=0
TEST_INITIAL_RESULT=checkpoint TEST_HEARTBEAT_STATUS=due ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>heartbeat-wake || status=$?
[[ "$status" == 2 ]]
grep -F 'heartbeat: primary' heartbeat-wake
grep -F 'Waiting on you' heartbeat-wake
grep -F 'PushNotification' heartbeat-wake
status=0
TEST_HEARTBEAT_STATUS=due ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>inbox-priority || status=$?
[[ "$status" == 2 ]]
grep -F 'A Tincan message is pending' inbox-priority
if grep -F 'heartbeat: primary' inbox-priority; then exit 1; fi
status=0
TEST_INITIAL_RESULT=checkpoint TEST_REMINDER_STATUS=quiet TEST_HEARTBEAT_PAUSED=1 ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>paused-inbox-wake || status=$?
[[ "$status" == 2 ]]
grep -F 'A Tincan message is pending' paused-inbox-wake
if grep -F 'heartbeat: primary' paused-inbox-wake; then exit 1; fi
status=0
TEST_INITIAL_RESULT=checkpoint TEST_REMINDER_STATUS=due TEST_HEARTBEAT_STATUS=due ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>reminder-priority || status=$?
[[ "$status" == 2 ]]
if grep -F 'heartbeat: primary' reminder-priority; then exit 1; fi
status=0
TEST_REMINDER_STATUS=due ./.agents/hooks/concierge-claude-stop <<< "$active_payload" 2>operator-wake || status=$?
[[ "$status" == 2 ]]
grep -F 'Waiting on you' operator-wake
grep -F 'PushNotification' operator-wake
output="$(TEST_REMINDER_STATUS=quiet ./.agents/hooks/concierge-claude-stop <<< "$active_payload" 2>&1)"
[[ -z "$output" ]]
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
