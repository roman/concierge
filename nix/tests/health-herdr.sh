case "$1 $2" in
  'pane get') printf '{"result":{"pane":{"pane_id":"p1","foreground_cwd":"%s","workspace_id":"w1","tab_id":"t1"}}}\n' "$SMOKE_WORKTREE" ;;
  'agent get') printf '%s\n' '{"result":{"agent":{"pane_id":"p1","agent_status":"working","agent":"codex"}}}' ;;
  'pane process-info') printf '{"result":{"type":"pane_process_info","process_info":{"pane_id":"p1","shell_pid":%s,"foreground_processes":%s}}}\n' "$SMOKE_SHELL_PID" "$SMOKE_FOREGROUND" ;;
  'status --json') printf '%s\n' '{"server":{"running":true}}' ;;
  *) exit 71 ;;
esac
