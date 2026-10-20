payload="$(cat)"
command -v jq >/dev/null || exit 0

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

printf '%s' "$payload" | "$concierge_command" --root "$repository_root/.concierge" --message-root "$repository_root/.tincan" heartbeat wait
