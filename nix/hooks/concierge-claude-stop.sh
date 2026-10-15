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
active_pending=0
while (( SECONDS < deadline )); do
  if ! reminder="$("$concierge_command" --root "$repository_root/.concierge" operator remind)"; then
    exit 0
  fi
  if [[ "$reminder" == $'status: due\n'* ]]; then
    printf '%s\n' "$reminder" >&2
    exit 2
  fi

  if (( active_pending )); then
    # An unhandled inbox message must not cause an immediate repeated wake.
    [[ "$reminder" != *$'open_items: 0\n'* && "$reminder" != *'open_items: 0' ]] || exit 0
    sleep 30
  fi
  if ! initial="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 1ms)"; then
    exit 0
  fi
  if [[ "$initial" == $'status: pending\n'* ]]; then
    if printf '%s' "$payload" | jq -e '.stop_hook_active == true' >/dev/null; then
      active_pending=1
      continue
    fi
    break
  fi
  active_pending=0
  if ! result="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 30s)"; then
    exit 0
  fi
  if [[ "$result" == $'status: pending\n'* ]]; then
    break
  fi
done
(( SECONDS < deadline )) || exit 0

printf 'A Tincan message is pending. Run "%s" --root "%s/.tincan" inbox list concierge, read each message, handle it, then acknowledge it.\n' "$tincan_command" "$repository_root" >&2
exit 2
