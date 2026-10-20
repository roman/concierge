set -euo pipefail

# Noninteractive tools and workers retain the native entry path.
if [[ -n "${CONCIERGE_WORKER:-}" ]]; then
  exec "$native_codex" "$@"
fi
for argument in "$@"; do
  case "$argument" in
    --remote|--remote=*|--no-daemon)
      printf 'Concierge Codex owns its native endpoint. Use the native binary for an explicit remote or no-daemon session.\n' >&2
      exit 2
      ;;
    --help|-h|--version|-V|exec|e|review|login|logout|mcp|plugin|app-server|remote-control|completion|update|doctor|sandbox|debug|apply|a|queue|archive|delete|migrate-rollouts|unarchive|cloud|exec-server|features|help|agents)
      exec "$native_codex" "$@"
      ;;
  esac
done

umask 077
session_directory="$(mktemp -d /tmp/concierge-codex.XXXXXXXX)"
export CONCIERGE_CODEX_SOCKET="$session_directory/native.sock"
server_pid=
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf -- "$session_directory"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# The launcher supplies only the transport. Native configuration, permissions,
# credentials, project trust, and hook trust still apply.
"$native_codex" app-server --listen "unix://$CONCIERGE_CODEX_SOCKET" >"$session_directory/server.log" 2>&1 &
server_pid=$!
for attempt in {1..100}; do
  [[ -S "$CONCIERGE_CODEX_SOCKET" ]] && break
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$session_directory/server.log" >&2
    exit 1
  fi
  sleep 0.05
done
if [[ ! -S "$CONCIERGE_CODEX_SOCKET" ]]; then
  printf 'Concierge Codex startup timed out. Native server output:\n' >&2
  cat "$session_directory/server.log" >&2
  exit 1
fi
"$native_codex" --remote "unix://$CONCIERGE_CODEX_SOCKET" "$@"
