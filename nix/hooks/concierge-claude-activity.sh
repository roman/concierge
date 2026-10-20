payload="$(cat)"
command -v jq >/dev/null || exit 1
if ! printf '%s' "$payload" | jq -e '
  (.hook_event_name == "UserPromptSubmit" or .hook_event_name == "Stop" or
   .hook_event_name == "PermissionRequest" or .hook_event_name == "StopFailure") and
  (.session_id | type == "string" and length > 0) and
  (has("cursor_version") | not) and
  ((.transcript_path // "") | contains("/.pi/") | not)
' >/dev/null 2>&1; then
  exit 1
fi
if [[ -n "${CLAUDE_PROJECT_DIR:-}" && "$(cd "$CLAUDE_PROJECT_DIR" && pwd -P)" != "$repository_root" ]]; then
  exit 1
fi
printf '%s' "$payload" | "$concierge_command" --root "$repository_root/.concierge" heartbeat activity
