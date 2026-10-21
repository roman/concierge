package sessionusage

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type windowPosition uint8

const (
	outsideWindow windowPosition = iota
	withinWindow
)

type turn struct {
	wake       bool
	promptSeen bool
	active     bool
}

type request struct {
	turnID string
	usage  Totals
}

type call struct {
	categories []string
}

type readerState struct {
	options       Options
	session       Session
	active        bool
	currentTurn   string
	turns         map[string]*turn
	requests      map[string]request
	calls         map[string]call
	seenCalls     map[string]bool
	seenResults   map[string]bool
	intervalReads int64
	unattributed  int64
}

func newReaderState(options Options, identity sessionIdentity) *readerState {
	return &readerState{
		options: options,
		session: Session{
			ID:                           identity.id,
			Parent:                       identity.parent,
			Metrics:                      newMetrics(options.Harness),
			SkillReadsBetweenCompactions: []int64{},
		},
		turns:       map[string]*turn{},
		requests:    map[string]request{},
		calls:       map[string]call{},
		seenCalls:   map[string]bool{},
		seenResults: map[string]bool{},
	}
}

func (s *readerState) inWindow(timestamp time.Time) bool {
	return !timestamp.Before(s.options.From) && !timestamp.After(s.options.To)
}

func (s *readerState) turn(id string) *turn {
	if id == "" {
		id = "unknown"
	}
	if s.turns[id] == nil {
		s.turns[id] = &turn{}
	}
	return s.turns[id]
}

func (s *readerState) consume(record object) {
	timestamp, err := time.Parse(time.RFC3339Nano, str(record["timestamp"]))
	if err != nil {
		// Known metadata has no time window. Unknown records still need a count.
		switch str(record["type"]) {
		case "session_meta",
			"last-prompt",
			"mode",
			"permission-mode",
			"atis-latch",
			"file-history-snapshot",
			"ai-title",
			"cost-state",
			"summary":
		default:
			s.unattributed++
		}
		return
	}
	inside := outsideWindow
	if s.inWindow(timestamp) {
		inside = withinWindow
	}
	if inside == withinWindow {
		s.active = true
	}
	if s.options.Harness == "codex" {
		s.codex(record, timestamp, inside)
	} else {
		s.claude(record, inside)
	}
}

func (s *readerState) prompt(text string) {
	t := s.turn(s.currentTurn)
	if t.promptSeen {
		return
	}
	t.promptSeen = true
	t.wake = t.wake || strings.HasPrefix(strings.TrimSpace(text), "Concierge supervisor event ")
}

func (s *readerState) usage(id, turnID string, usage Totals, inside windowPosition) {
	if id == "" {
		if inside == withinWindow {
			s.session.SkippedRecords++
		}
		return
	}
	if previous, exists := s.requests[id]; exists {
		if inside == withinWindow {
			s.session.DuplicateUsage++
		}
		// Claude saves several content blocks with one message ID. Later blocks
		// can carry the completed usage snapshot for that same request.
		if inside == withinWindow && s.options.Harness == "claude" && previous.usage.Requests > 0 {
			usage.Requests = 1
			usage.UncachedInput = usage.Input - usage.CachedInput
			s.requests[id] = request{previous.turnID, usage}
		}
		return
	}
	// Remember out-of-window IDs so a replay cannot move a request into the window.
	s.requests[id] = request{}
	if inside == outsideWindow {
		return
	}
	usage.Requests = 1
	usage.UncachedInput = usage.Input - usage.CachedInput
	s.requests[id] = request{turnID, usage}
	s.turn(turnID).active = true
}

func (s *readerState) compact(inside windowPosition) {
	if inside == outsideWindow {
		return
	}
	s.session.Compactions++
	s.session.SkillReadsBetweenCompactions = append(
		s.session.SkillReadsBetweenCompactions,
		s.intervalReads,
	)
	s.intervalReads = 0
}

func (s *readerState) codex(record object, timestamp time.Time, inside windowPosition) {
	p := obj(record["payload"])
	switch str(record["type"]) {
	case "session_meta", "world_state":
	case "turn_context":
		if id := str(p["turn_id"]); id != "" {
			s.currentTurn = id
		}
	case "compacted":
		s.compact(inside)
	case "token_usage_record":
		u := obj(p["usage"])
		if u == nil {
			if inside == withinWindow {
				s.session.SkippedRecords++
			}
			return
		}
		s.usage(
			str(p["response_id"]),
			str(p["turn_id"]),
			Totals{
				Input:       number(u["input_tokens"]),
				CachedInput: number(u["cached_input_tokens"]),
				Output:      number(u["output_tokens"]),
				Reasoning:   number(u["reasoning_output_tokens"]),
			},
			inside,
		)
	case "event_msg":
		switch str(p["type"]) {
		case "task_started":
			s.currentTurn = str(p["turn_id"])
			t := s.turn(s.currentTurn)
			t.active = t.active || inside == withinWindow
			trigger := str(obj(p["turn_attribution"])["turn_trigger"])
			t.wake = trigger == "wake" || trigger == "heartbeat"
		case "token_count":
			if inside == withinWindow {
				s.quota(obj(p["rate_limits"]), timestamp)
			}
		case "task_complete",
			"user_message",
			"agent_message",
			"agent_reasoning",
			"agent_reasoning_raw_content",
			"agent_reasoning_section_break",
			"item_completed",
			"thread_settings_applied",
			"context_compacted",
			"exec_command_begin",
			"exec_command_end",
			"turn_aborted",
			"warning",
			"error":
		default:
			if inside == withinWindow {
				s.session.SkippedRecords++
			}
		}
	case "response_item":
		switch str(p["type"]) {
		case "message":
			if str(p["role"]) == "user" {
				s.prompt(contentText(p["content"]))
			}
		case "function_call", "custom_tool_call":
			arguments := p["arguments"]
			if arguments == nil {
				arguments = p["input"]
			}
			s.toolCall(str(p["call_id"]), str(p["name"]), arguments, inside)
		case "function_call_output", "custom_tool_call_output":
			s.toolResult(str(p["call_id"]), p["output"], inside)
		case "reasoning", "compaction", "web_search_call", "image_generation_call":
		default:
			if inside == withinWindow {
				s.session.SkippedRecords++
			}
		}
	default:
		if inside == withinWindow {
			s.session.SkippedRecords++
		}
	}
}

func (s *readerState) quota(limits object, timestamp time.Time) {
	for _, key := range []string{"primary", "secondary"} {
		limit := obj(limits[key])
		if number(limit["window_minutes"]) != 10080 {
			continue
		}
		percent, ok := limit["used_percent"].(float64)
		if ok {
			s.session.WeeklyQuota.observe(&Observation{timestamp, percent})
		}
	}
}

func (s *readerState) claude(record object, inside windowPosition) {
	message := obj(record["message"])
	switch str(record["type"]) {
	case "user":
		content := message["content"]
		results := false
		for _, item := range array(content) {
			block := obj(item)
			if str(block["type"]) != "tool_result" {
				continue
			}
			results = true
			id := str(block["tool_use_id"])
			if inside == withinWindow && block["is_error"] == true && !s.seenResults[id] {
				*s.session.ToolErrors++
			}
			s.toolResult(id, block["content"], inside)
		}
		if results || record["isMeta"] == true {
			return
		}
		id := str(record["uuid"])
		if id == "" {
			if inside == withinWindow {
				s.session.SkippedRecords++
			}
			return
		}
		s.currentTurn = id
		s.turn(id).active = s.turn(id).active || inside == withinWindow
		s.prompt(contentText(content))
	case "assistant":
		if usage := obj(message["usage"]); usage != nil {
			s.usage(
				str(message["id"]),
				s.currentTurn,
				Totals{
					Input: number(
						usage["input_tokens"],
					) + number(
						usage["cache_creation_input_tokens"],
					) + number(
						usage["cache_read_input_tokens"],
					),
					CachedInput: number(usage["cache_read_input_tokens"]),
					Output:      number(usage["output_tokens"]),
					Reasoning:   number(obj(usage["output_tokens_details"])["thinking_tokens"]),
				},
				inside,
			)
		}
		for _, item := range array(message["content"]) {
			block := obj(item)
			if str(block["type"]) == "tool_use" {
				s.toolCall(str(block["id"]), str(block["name"]), block["input"], inside)
			}
		}
	case "system":
		if str(record["subtype"]) == "compact_boundary" {
			s.compact(inside)
		}
	case "last-prompt",
		"mode",
		"permission-mode",
		"atis-latch",
		"attachment",
		"file-history-snapshot",
		"ai-title",
		"queue-operation",
		"cost-state",
		"progress",
		"summary":
	default:
		if inside == withinWindow {
			s.session.SkippedRecords++
		}
	}
}

func contentText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	parts := []string{}
	for _, item := range array(value) {
		block := obj(item)
		if text, ok := block["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

var commandPatterns = map[string]*regexp.Regexp{
	"claim":       regexp.MustCompile(`\bheartbeat\s+claim\b`),
	"reply_check": regexp.MustCompile(`\btask\s+reply\s+check\b`),
	"report":      regexp.MustCompile(`\btask\s+report\b`),
}

var escalationPattern = regexp.MustCompile(
	`(?:"sandbox_permissions"|sandbox_permissions)\s*:\s*"require_escalated"`,
)
var nativeExitPattern = regexp.MustCompile(`Process exited with code ([1-9][0-9]*)\b`)

func argumentText(value any) string {
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return argumentText(decoded)
		}
		return text
	}
	if m := obj(value); m != nil {
		parts := []string{}
		for _, key := range []string{"cmd", "command", "input", "file_path", "path"} {
			if text := str(m[key]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func escalationCount(value any) int64 {
	if list, ok := value.([]any); ok {
		var count int64
		for _, field := range list {
			count += escalationCount(field)
		}
		return count
	}
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return escalationCount(decoded)
		}
		return int64(len(escalationPattern.FindAllString(text, -1)))
	}
	var count int64
	for key, field := range obj(value) {
		if key == "sandbox_permissions" && str(field) == "require_escalated" {
			count++
		}
		if obj(field) != nil || array(field) != nil {
			count += escalationCount(field)
		}
	}
	return count
}

func (s *readerState) toolCall(id, name string, arguments any, inside windowPosition) {
	if id == "" || s.seenCalls[id] {
		return
	}
	s.seenCalls[id] = true
	text := argumentText(arguments)
	categories := []string{}
	for category, pattern := range commandPatterns {
		if !pattern.MatchString(text) {
			continue
		}
		categories = append(categories, category)
		if inside == withinWindow {
			metric := s.session.ToolOutput[category]
			metric.Calls++
			s.session.ToolOutput[category] = metric
		}
	}
	s.calls[id] = call{categories}
	if inside == outsideWindow {
		return
	}
	if s.session.RequireEscalated != nil {
		*s.session.RequireEscalated += escalationCount(arguments)
	}
	if strings.Contains(text, "concierge-local-supervisor/SKILL.md") &&
		(name == "Read" || strings.Contains(text, "cat ") || strings.Contains(text, "sed ") || strings.Contains(text, "read_file") || strings.Contains(text, "head ")) {
		s.session.SkillReads++
		s.intervalReads++
	}
}

func (s *readerState) toolResult(id string, value any, inside windowPosition) {
	if id == "" || s.seenResults[id] {
		return
	}
	s.seenResults[id] = true
	if inside == outsideWindow {
		return
	}
	text := contentText(value)
	for _, category := range s.calls[id].categories {
		metric := s.session.ToolOutput[category]
		metric.Results++
		metric.Bytes += int64(len(text))
		s.session.ToolOutput[category] = metric
	}
	if s.session.NonzeroCommands != nil {
		*s.session.NonzeroCommands += nonzeroResults(value)
	}
}

// Codex exec wrappers contain JSON text blocks. Decode those before inspecting exits.
func nonzeroResults(value any) int64 {
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return nonzeroResults(decoded)
		}
		return int64(len(nativeExitPattern.FindAllString(text, -1)))
	}
	if list, ok := value.([]any); ok {
		var count int64
		for _, item := range list {
			count += nonzeroResults(item)
		}
		return count
	}
	var count int64
	for key, field := range obj(value) {
		if key == "exit_code" && number(field) != 0 {
			count++
		}
		if key == "text" || key == "content" {
			count += nonzeroResults(field)
		}
	}
	return count
}

func (s *readerState) finish() Session {
	for _, request := range s.requests {
		if request.usage.Requests == 0 {
			continue
		}
		s.session.Usage.add(request.usage)
		if s.turn(request.turnID).wake {
			s.session.WakeUsage.add(request.usage)
		}
	}
	for _, turn := range s.turns {
		if !turn.active {
			continue
		}
		s.session.Turns++
		if turn.wake {
			s.session.WakeTurns++
		}
	}
	s.session.SkillReadsBetweenCompactions = append(
		s.session.SkillReadsBetweenCompactions,
		s.intervalReads,
	)
	s.session.Metrics.finish()
	return s.session
}

func (t Totals) String() string {
	return fmt.Sprintf(
		"%d requests, %d input, %d cached, %d output",
		t.Requests,
		t.Input,
		t.CachedInput,
		t.Output,
	)
}
