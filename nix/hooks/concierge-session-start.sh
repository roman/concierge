if [[ ! -f "$repository_root/.concierge/.task-state-version" ]] || [[ "$(cat "$repository_root/.concierge/.task-state-version")" != 2 ]]; then
  printf 'Concierge version 2 state is required. Run concierge state init for fresh state; inspect existing state before migration.\n'
  exit 0
fi

payload="$(cat)"
if primary_result="$(printf '%s' "$payload" | "$concierge_command" --root "$repository_root/.concierge" primary claim 2>&1)"; then
  [[ "$primary_result" == $'status: owner\n'* ]] || exit 0
elif [[ "$primary_result" == $'status: held_by_other\n'* ]]; then
  printf 'Concierge secondary session. The live primary session supervises local work.\n'
  exit 0
else
  printf 'Concierge startup failed: %s\n' "$primary_result"
  exit 0
fi

host_args=(--root "$repository_root/.concierge" --message-root "$repository_root/.tincan" host)
if [[ "${HERDR_ENV:-}" == 1 ]]; then
  host_args+=(--session "${HERDR_SESSION:-default}")
fi
host_status=0
host_result="$("$concierge_command" "${host_args[@]}" status --timeout 1s 2>&1)" || host_status=$?
if [[ "$host_status" == 1 ]]; then
  mkdir -p "$repository_root/.concierge"
  host_log="$repository_root/.concierge/host.log"
  if [[ -L "$host_log" || ( -e "$host_log" && ! -f "$host_log" ) ]]; then
    printf 'Concierge host unavailable: host.log must be a regular file.\n'
    exit 0
  fi
  if ! (umask 077; : >>"$host_log") || ! chmod 600 "$host_log"; then
    printf 'Concierge host unavailable: cannot secure host.log.\n'
    exit 0
  fi
  # The host must survive the root session and release the hook's output pipes.
  if ! (umask 077; nohup "$concierge_command" "${host_args[@]}" serve </dev/null >>"$host_log" 2>&1 &); then
    printf 'Concierge host unavailable: detached launch failed. Inspect .concierge/host.log.\n'
    exit 0
  fi
  for attempt in {1..6}; do
    host_status=0
    host_result="$("$concierge_command" "${host_args[@]}" status --timeout 1s 2>&1)" || host_status=$?
    [[ "$host_status" == 1 || "$host_status" == 3 ]] || break
    sleep 0.1
  done
fi
if [[ "$host_status" != 0 ]]; then
  printf 'Concierge host unavailable: %s\nA manual restart is required after a package update. Inspect .concierge/host.log and the running host before supervising work.\n' "$host_result"
  exit 0
fi

if ! brief="$("$concierge_command" --root "$repository_root/.concierge" --message-root "$repository_root/.tincan" brief 2>&1)"; then
  printf 'Concierge startup failed: %s\n' "$brief"
  exit 0
fi

printf 'Concierge root supervisor context\n%s\n' "$brief"
printf 'concierge_bin: %s\n' "${concierge_command%/*}"
printf 'tincan_bin: %s\n' "${tincan_command%/*}"
printf 'Use .agents/skills/concierge-local-supervisor/SKILL.md for local wakes, workers, and task supervision.\n'
