payload="$(cat)"
command -v jq >/dev/null || exit 0
command -v flock >/dev/null || exit 0

if ! printf '%s' "$payload" | jq -e '
  .hook_event_name == "Stop" and
  (.session_id | type == "string" and length > 0) and
  (has("cursor_version") | not) and
  ((.transcript_path // "") | contains("/.pi/") | not)
' >/dev/null 2>&1; then
  exit 0
fi

if [[ -n "${CLAUDE_PROJECT_DIR:-}" && "$(cd "$CLAUDE_PROJECT_DIR" && pwd -P)" != "$repository_root" ]]; then
  exit 0
fi

if ! primary_result="$(printf '%s' "$payload" | "$concierge_command" --root "$repository_root/.concierge" primary verify 2>&1)"; then
  exit 0
fi
[[ "$primary_result" == $'status: owner\n'* ]] || exit 0

mkdir -p "$repository_root/.concierge"
exec {lock_fd}>"$repository_root/.concierge/claude-stop.lock"
flock -n "$lock_fd" || exit 0

deadline=$((SECONDS + 28740))
while (( SECONDS < deadline )); do
  active_pending=0
  if ! initial="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 1ms)"; then
    exit 0
  fi
  if [[ "$initial" == $'status: pending\n'* ]]; then
    if printf '%s' "$payload" | jq -e '.stop_hook_active == true' >/dev/null; then
      active_pending=1
    else
      break
    fi
  else
    active_pending=0
  fi

  if ! reminder="$("$concierge_command" --root "$repository_root/.concierge" operator remind)"; then
    exit 0
  fi
  if [[ "$reminder" == $'status: due\n'* ]]; then
    "$concierge_command" --root "$repository_root/.concierge" heartbeat check --wake reminder >/dev/null || exit 0
    printf '%s\n' "$reminder" >&2
    exit 2
  fi

  if ! heartbeat="$("$concierge_command" --root "$repository_root/.concierge" --message-root "$repository_root/.tincan" heartbeat check)"; then
    exit 0
  fi
  if [[ "$heartbeat" == $'status: due\n'* ]]; then
    printf '%s\n' "$heartbeat" >&2
    exit 2
  fi
  if (( active_pending )); then
    # Preserve periodic status without repeatedly waking for the same message.
    [[ "$heartbeat" != *'active: no'* ]] || exit 0
    sleep 30
    continue
  fi
  if ! result="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 30s)"; then
    exit 0
  fi
  if [[ "$result" == $'status: pending\n'* ]]; then
    break
  fi
done
(( SECONDS < deadline )) || exit 0

"$concierge_command" --root "$repository_root/.concierge" heartbeat check --wake inbox >/dev/null || exit 0
printf 'A Tincan message is pending. Run "%s" --root "%s/.tincan" inbox list concierge, read each message, handle it, then acknowledge it.\n' "$tincan_command" "$repository_root" >&2
exit 2
