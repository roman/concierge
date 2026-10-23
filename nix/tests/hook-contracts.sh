#!/usr/bin/env bash
set -euo pipefail

unset HERDR_ENV HERDR_SESSION
# Run the committed commands outside the project, including a different repo.
export CLAUDE_PROJECT_DIR="$DEVENV_ROOT"
foreign_project="$TMPDIR/other-repository"
git -c init.templateDir= init -q "$foreign_project"
for directory in "$DEVENV_ROOT/subdirectory" "$foreign_project"; do
  for settings in .claude/settings.json .codex/hooks.json; do
    hook_commands="$(jq -r '.hooks | to_entries[] | .key as $event | .value[].hooks[] | [$event, .command] | join("|")' "$DEVENV_ROOT/$settings")"
    [[ -n "$hook_commands" ]]
    while IFS='|' read -r event command; do
      : > "$TEST_CALLS"
      status=0
      (cd "$directory" && TEST_HOST_STATUS=0 TEST_CONSUMER_STATUS=0 bash -c "$command" \
        <<< '{"hook_event_name":"'"$event"'","session_id":"fixture"}') > "$TMPDIR/foreign-hook-output" 2>&1 || status=$?
      if [[ "$status" != 0 || ! -s "$TEST_CALLS" ]]; then
        cat "$TMPDIR/foreign-hook-output" >&2
        printf 'hook did not run: %s %s from %s (exit %s)\n' "$settings" "$event" "$directory" "$status" >&2
        exit 1
      fi
      grep -F -- "--root $DEVENV_ROOT/.concierge" "$TEST_CALLS"
      : > "$TEST_CALLS"
      (cd "$directory" && CONCIERGE_WORKER=fixture CLAUDE_PROJECT_DIR=/missing CONCIERGE_PROJECT_ROOT=/missing \
        bash -c "$command" </dev/null)
      [[ ! -s "$TEST_CALLS" ]]
    done <<< "$hook_commands"
  done
done
unset CLAUDE_PROJECT_DIR
payload='{"hook_event_name":"SessionStart","session_id":"fixture"}'
# Keep the default output byte-for-byte stable, apart from package paths.
TEST_HOST_STATUS=0 ./.agents/hooks/concierge-session-start <<< "$payload" > "$TMPDIR/default-start"
concierge_bin="$(dirname "$(command -v concierge)")"
tincan_bin="$(dirname "$(command -v tincan)")"
sed -e "s|@conciergeBin@|$concierge_bin|" -e "s|@tincanBin@|$tincan_bin|" \
  "$defaultStartFixture" > "$TMPDIR/expected-default-start"
cmp "$TMPDIR/expected-default-start" "$TMPDIR/default-start"

# A caller contract precedes the brief and survives hostile runtime inputs.
{
  head -n 3 "$TMPDIR/expected-default-start"
  printf 'Caller contract: fixture-contract (sha256 %s)\n' "$(sha256sum "$contractFixture" | cut -d ' ' -f 1)"
  cat "$contractFixture"
  printf '\n'
  tail -n +4 "$TMPDIR/expected-default-start"
} > "$TMPDIR/expected-contract-start"
TEST_HOST_STATUS=0 CONCIERGE_PRIMARY_CONTRACT='' CONCIERGE_PRIMARY_CONTRACT_SOURCE=/missing \
  CONCIERGE_PRIMARY_CONTRACT_ID=override CONCIERGE_PRIMARY_CONTRACT_TEXT=override \
  CONCIERGE_WORKER_BRIEF=override "$contractHook" <<< "$payload" > "$TMPDIR/contract-start"
cmp "$TMPDIR/expected-contract-start" "$TMPDIR/contract-start"
TEST_HOST_STATUS=0 "$storeStringContractHook" <<< "$payload" > "$TMPDIR/store-string-contract-start"
cmp "$TMPDIR/expected-contract-start" "$TMPDIR/store-string-contract-start"
TEST_HOST_STATUS=0 "$storeFileContractHook" <<< "$payload" > "$TMPDIR/store-file-contract-start"
cmp "$TMPDIR/expected-contract-start" "$TMPDIR/store-file-contract-start"
test ! -e "$DEVENV_ROOT/contract-executed"
mkdir -p "$DEVENV_ROOT/.concierge/config"
printf 'Caller contract: brief-include override\n' > "$DEVENV_ROOT/.concierge/config/brief-include.md"
TEST_HOST_STATUS=0 TEST_BRIEF='Caller contract: worker brief override' \
  "$contractHook" <<< "$payload" > "$TMPDIR/hostile-brief-start"
grep -Fx 'Caller contract: worker brief override' "$TMPDIR/hostile-brief-start"
grep -Fx 'Caller contract: brief-include override' "$TMPDIR/hostile-brief-start"
sed '/^Concierge revisions:/,$d' "$TMPDIR/expected-contract-start" > "$TMPDIR/expected-contract-prefix"
sed '/^Concierge revisions:/,$d' "$TMPDIR/hostile-brief-start" > "$TMPDIR/hostile-brief-prefix"
cmp "$TMPDIR/expected-contract-prefix" "$TMPDIR/hostile-brief-prefix"
rm "$DEVENV_ROOT/.concierge/config/brief-include.md"
CONCIERGE_WORKER=fixture "$contractHook" <<< "$payload" > "$TMPDIR/worker-contract-start"
test ! -s "$TMPDIR/worker-contract-start"
TEST_PRIMARY_STATUS=held_by_other "$contractHook" <<< "$payload" > "$TMPDIR/secondary-contract-start"
printf 'Concierge secondary session. The live primary session supervises local work.\n' > "$TMPDIR/expected-secondary-start"
cmp "$TMPDIR/expected-secondary-start" "$TMPDIR/secondary-contract-start"

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

: > "$TEST_CALLS"
output="$(TEST_PRIMARY_KIND=codex TEST_HOST_STATUS=0 ./.agents/hooks/concierge-session-start <<< "$payload")"
grep -F 'heartbeat start' "$TEST_CALLS"
[[ "$output" == *'Concierge Codex heartbeat: registered'* ]]
output="$(TEST_PRIMARY_KIND=codex TEST_START_STATUS=1 TEST_HOST_STATUS=0 ./.agents/hooks/concierge-session-start <<< "$payload")"
[[ "$output" == *'Concierge Codex heartbeat unavailable:'* ]]
[[ "$output" == *'Concierge root supervisor context'* ]]

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
printf -v restart_command '%q ' concierge --root "$DEVENV_ROOT/.concierge" --message-root "$DEVENV_ROOT/.tincan" host restart
[[ "$output" == *"$restart_command"* ]]
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
for extra in '' ',"stop_hook_active":true'; do
  status=0
  body='{"hook_event_name":"Stop","session_id":"fixture"'"$extra"'}'
  ./.agents/hooks/concierge-claude-stop <<< "$body" 2>event-wake || status=$?
  [[ "$status" == 2 ]]
  grep -F 'Concierge supervisor event fixture' event-wake
  output="$(TEST_CONSUMER_STATUS=0 ./.agents/hooks/concierge-claude-stop <<< "$body" 2>&1)"
  [[ -z "$output" ]]
done
output="$(TEST_PRIMARY_STATUS=held_by_other ./.agents/hooks/concierge-claude-stop <<< "$payload" 2>&1)"
[[ -z "$output" ]]
for foreign_payload in \
  '{"hook_event_name":"Stop","session_id":"fixture","cursor_version":"fixture"}' \
  '{"hook_event_name":"Stop","session_id":"fixture","transcript_path":"/tmp/.pi/sessions/fixture.jsonl"}'; do
  output="$(./.agents/hooks/concierge-claude-stop <<< "$foreign_payload" 2>&1)"
  [[ -z "$output" ]]
done
for event in UserPromptSubmit Stop PermissionRequest StopFailure; do
  ./.agents/hooks/concierge-claude-activity <<< '{"hook_event_name":"'"$event"'","session_id":"fixture"}'
done
status=0
TEST_ACTIVITY_STATUS=1 ./.agents/hooks/concierge-claude-activity <<< '{"hook_event_name":"UserPromptSubmit","session_id":"fixture"}' || status=$?
[[ "$status" == 1 ]]
: > "$TEST_CALLS"
CONCIERGE_WORKER=fixture ./.agents/hooks/concierge-claude-activity <<< "$payload"
[[ ! -s "$TEST_CALLS" ]]
TEST_CONSUMER_STATUS=0 ./.agents/hooks/concierge-claude-stop <<< "$payload"
grep -F 'heartbeat wait' "$TEST_CALLS"
if grep -E 'heartbeat check|operator remind|inbox wait' "$TEST_CALLS"; then
  printf 'native consumer started an independent clock\n' >&2
  exit 1
fi
