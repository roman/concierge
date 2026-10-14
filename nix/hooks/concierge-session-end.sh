payload="$(cat)"
if ! result="$(printf '%s' "$payload" | "$concierge_command" --root "$repository_root/.concierge" primary release 2>&1)"; then
  [[ "$result" == $'status: held_by_other\n'* ]] || printf 'Concierge release failed: %s\n' "$result" >&2
fi
