#!/usr/bin/env bash
set -euo pipefail

# Exact guards remain part of the committed command contract.
eval "$extendedGuardShellEntry" 2>mismatched-guard-warning
grep -F '.claude/settings.json (SessionStart)' mismatched-guard-warning
grep -F '.codex/hooks.json (SessionEnd)' mismatched-guard-warning

default_guard='[ -z "${CONCIERGE_WORKER:-}" ]'
for settings in .claude/settings.json .codex/hooks.json; do
  jq --arg guard "$default_guard" --arg replacement "$extendedHookGuard" \
    'walk(if type == "string" then split($guard) | join($replacement) else . end)' \
    "$settings" > extended-hooks.json
  cp extended-hooks.json "$settings"
done
cp .claude/settings.json extended-settings.json

eval "$extendedGuardShellEntry" 2>extended-guard-warning
if grep -F 'hook settings need review' extended-guard-warning; then exit 1; fi

# A caller guard does not relax command or native hook option checks.
while IFS='|' read -r event mutation; do
  jq "$mutation" extended-settings.json > .claude/settings.json
  eval "$extendedGuardShellEntry" 2>invalid-extended-settings-warning
  grep -F ".claude/settings.json ($event)" invalid-extended-settings-warning
done <<'CASES'
SessionStart|.hooks.SessionStart[0].hooks[0].command += " unsafe-command"
SessionStart|.hooks.SessionStart[0].hooks[0].type = "prompt"
Stop|.hooks.Stop[0].hooks[1].asyncRewake = false
Stop|.hooks.Stop[0].hooks[1].timeout = 15
Stop|.hooks.Stop[0].hooks[1].timeout = "28800"
Stop|.hooks.Stop[0].hooks[0].async = true
Stop|.hooks.Stop[0].hooks[0].asyncRewake = true
UserPromptSubmit|del(.hooks.UserPromptSubmit[0].hooks[0].onFailure)
CASES

# Guard text must not select the native hook option checks.
cp extended-settings.json .claude/settings.json
for settings in .claude/settings.json .codex/hooks.json; do
  jq --arg guard "$extendedHookGuard" --arg replacement "$activityNamedGuard" \
    'walk(if type == "string" then split($guard) | join($replacement) else . end)' \
    "$settings" > activity-named-hooks.json
  cp activity-named-hooks.json "$settings"
done
cp .claude/settings.json activity-named-settings.json
eval "$activityNamedGuardShellEntry" 2>activity-named-guard-warning
if grep -F 'hook settings need review' activity-named-guard-warning; then exit 1; fi
jq '.hooks.Stop[0].hooks[1] |= (.asyncRewake = false | .timeout = 15)' \
  activity-named-settings.json > .claude/settings.json
eval "$activityNamedGuardShellEntry" 2>activity-named-short-stop-warning
grep -F '.claude/settings.json (Stop)' activity-named-short-stop-warning

cp guarded-settings.json .claude/settings.json
cp guarded-codex-hooks.json .codex/hooks.json
