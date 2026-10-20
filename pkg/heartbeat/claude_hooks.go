package heartbeat

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type claudeHook struct {
	Type        string  `json:"type"`
	Command     string  `json:"command"`
	Async       bool    `json:"async"`
	AsyncRewake bool    `json:"asyncRewake"`
	OnFailure   string  `json:"onFailure"`
	Timeout     float64 `json:"timeout"`
}

func ClaudeHookCommand(name string) string {
	return `if [ -z "${CONCIERGE_WORKER:-}" ]; then ./.agents/hooks/` + name + `; fi`
}

// ValidateClaudeHooks requires a synchronous readiness gate before each turn.
// An async Stop consumer alone can finish after a new prompt has started.
func ValidateClaudeHooks(project string) error {
	for _, name := range []string{"concierge-claude-activity", "concierge-claude-stop"} {
		info, err := os.Stat(filepath.Join(project, ".agents", "hooks", name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return errors.New("Claude lifecycle hooks must be executable regular files")
		}
	}
	file, err := os.OpenFile(
		filepath.Join(project, ".claude", "settings.json"),
		os.O_RDONLY|syscall.O_NONBLOCK,
		0,
	)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("Claude hook settings must be a regular file within one MiB")
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string       `json:"matcher"`
			Hooks   []claudeHook `json:"hooks"`
		} `json:"hooks"`
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("Claude hook settings exceed one MiB")
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	activity := ClaudeHookCommand("concierge-claude-activity")
	consumer := ClaudeHookCommand("concierge-claude-stop")
	has := func(event string, match func(claudeHook) bool) bool {
		for _, group := range settings.Hooks[event] {
			if group.Matcher != "" && group.Matcher != "*" {
				continue
			}
			for _, hook := range group.Hooks {
				if hook.Type == "command" && match(hook) {
					return true
				}
			}
		}
		return false
	}
	synchronous := func(hook claudeHook) bool { return hook.Command == activity && !hook.Async && !hook.AsyncRewake }
	if !has(
		"UserPromptSubmit",
		func(hook claudeHook) bool { return synchronous(hook) && hook.OnFailure == "block" },
	) ||
		!has("Stop", synchronous) ||
		!has("Stop", func(hook claudeHook) bool {
			return hook.Command == consumer && hook.AsyncRewake && hook.Timeout >= 28800
		}) {
		return errors.New(
			"Claude delivery unavailable: configure synchronous activity hooks for Stop and UserPromptSubmit (onFailure: block), plus the eight-hour asyncRewake Stop consumer",
		)
	}
	return nil
}
