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

if ! initial="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 1ms)"; then
  exit 0
fi
if [[ "$initial" == $'status: pending\n'* ]]; then
  # The first wake leaves its message pending until Claude handles it.
  if printf '%s' "$payload" | jq -e '.stop_hook_active == true' >/dev/null; then
    exit 0
  fi
else
  if ! result="$("$tincan_command" --root "$repository_root/.tincan" inbox wait concierge --timeout 7h59m)"; then
    exit 0
  fi
  [[ "$result" == $'status: pending\n'* ]] || exit 0
fi

printf 'A Tincan message is pending. Run "%s" --root "%s/.tincan" inbox list concierge, read each message, handle it, then acknowledge it.\n' "$tincan_command" "$repository_root" >&2
exit 2
