// Package sessionusage measures saved transcripts without contacting a harness or Concierge.
package sessionusage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Options struct {
	Harness   string
	Directory string
	Project   string
	From      time.Time
	To        time.Time
}

type Totals struct {
	Requests      int64 `json:"requests"`
	Input         int64 `json:"input"`
	CachedInput   int64 `json:"cached_input"`
	UncachedInput int64 `json:"uncached_input"`
	Output        int64 `json:"output"`
	Reasoning     int64 `json:"reasoning"`
}

func (t *Totals) add(other Totals) {
	t.Requests += other.Requests
	t.Input += other.Input
	t.CachedInput += other.CachedInput
	t.UncachedInput += other.UncachedInput
	t.Output += other.Output
	t.Reasoning += other.Reasoning
}

type OutputMetric struct {
	Calls        int64   `json:"calls"`
	Results      int64   `json:"results"`
	Bytes        int64   `json:"bytes"`
	BytesPerCall float64 `json:"bytes_per_call"`
}

type WakeAverage struct {
	Requests float64 `json:"requests"`
	Input    float64 `json:"input"`
	Output   float64 `json:"output"`
	Tokens   float64 `json:"tokens"`
}

type Observation struct {
	Time        time.Time `json:"time"`
	UsedPercent float64   `json:"used_percent"`
}

type Quota struct {
	First *Observation `json:"first"`
	Last  *Observation `json:"last"`
}

type Metrics struct {
	Usage            Totals                  `json:"usage"`
	Turns            int64                   `json:"turns"`
	WakeTurns        int64                   `json:"wake_turns"`
	WakeUsage        Totals                  `json:"wake_usage"`
	PerWakeTurn      WakeAverage             `json:"per_wake_turn"`
	ToolOutput       map[string]OutputMetric `json:"tool_output"`
	SkillReads       int64                   `json:"skill_reads"`
	Compactions      int64                   `json:"compactions"`
	RequireEscalated *int64                  `json:"require_escalated"`
	NonzeroCommands  *int64                  `json:"nonzero_commands"`
	ToolErrors       *int64                  `json:"tool_errors"`
	SkippedRecords   int64                   `json:"skipped_records"`
	DuplicateUsage   int64                   `json:"duplicate_usage"`
	WeeklyQuota      Quota                   `json:"weekly_quota"`
}

type Session struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
	Metrics
	SkillReadsBetweenCompactions []int64 `json:"skill_reads_between_compactions"`
}

type Report struct {
	Harness              string    `json:"harness"`
	Project              string    `json:"project"`
	From                 time.Time `json:"from"`
	To                   time.Time `json:"to"`
	Primary              Metrics   `json:"primary"`
	Children             Metrics   `json:"children"`
	Sessions             []Session `json:"sessions"`
	ChildSessions        []Session `json:"child_sessions"`
	SkippedFiles         int64     `json:"skipped_files"`
	UnattributedRecords  int64     `json:"unattributed_records"`
	PendingReplyTurnEnds string    `json:"pending_reply_turn_ends"`
	Limitations          []string  `json:"limitations"`
}

func newMetrics(harness string) Metrics {
	m := Metrics{ToolOutput: map[string]OutputMetric{"claim": {}, "reply_check": {}, "report": {}}}
	if harness == "codex" {
		m.RequireEscalated = new(int64)
		m.NonzeroCommands = new(int64)
	}
	if harness == "claude" {
		m.ToolErrors = new(int64)
	}
	return m
}

func (m *Metrics) finish() {
	if m.WakeTurns > 0 {
		n := float64(m.WakeTurns)
		m.PerWakeTurn = WakeAverage{
			float64(m.WakeUsage.Requests) / n,
			float64(m.WakeUsage.Input) / n,
			float64(m.WakeUsage.Output) / n,
			float64(m.WakeUsage.Input+m.WakeUsage.Output) / n,
		}
	}
	for key, metric := range m.ToolOutput {
		if metric.Calls > 0 {
			metric.BytesPerCall = float64(metric.Bytes) / float64(metric.Calls)
		}
		m.ToolOutput[key] = metric
	}
}

func (m *Metrics) add(other Metrics) {
	m.Usage.add(other.Usage)
	m.WakeUsage.add(other.WakeUsage)
	m.Turns += other.Turns
	m.WakeTurns += other.WakeTurns
	m.SkillReads += other.SkillReads
	m.Compactions += other.Compactions
	m.SkippedRecords += other.SkippedRecords
	m.DuplicateUsage += other.DuplicateUsage
	if m.RequireEscalated != nil {
		*m.RequireEscalated += *other.RequireEscalated
	}
	if m.NonzeroCommands != nil {
		*m.NonzeroCommands += *other.NonzeroCommands
	}
	if m.ToolErrors != nil {
		*m.ToolErrors += *other.ToolErrors
	}
	for key, metric := range other.ToolOutput {
		current := m.ToolOutput[key]
		current.Calls += metric.Calls
		current.Results += metric.Results
		current.Bytes += metric.Bytes
		m.ToolOutput[key] = current
	}
	m.WeeklyQuota.observe(other.WeeklyQuota.First)
	m.WeeklyQuota.observe(other.WeeklyQuota.Last)
}

func (q *Quota) observe(value *Observation) {
	if value == nil {
		return
	}
	if q.First == nil || value.Time.Before(q.First.Time) {
		q.First = value
	}
	if q.Last == nil || value.Time.After(q.Last.Time) {
		q.Last = value
	}
}

// Measure streams JSONL files. The inclusive window matches saved audit windows.
func Measure(options Options) (Report, error) {
	report := Report{
		Harness:              options.Harness,
		Project:              options.Project,
		From:                 options.From,
		To:                   options.To,
		Primary:              newMetrics(options.Harness),
		Children:             newMetrics(options.Harness),
		Sessions:             []Session{},
		ChildSessions:        []Session{},
		PendingReplyTurnEnds: "not derivable",
		Limitations:          limitations(options.Harness),
	}
	if options.Harness != "codex" && options.Harness != "claude" {
		return report, errors.New("harness must be codex or claude")
	}
	if options.From.IsZero() || options.To.IsZero() || options.To.Before(options.From) {
		return report, errors.New("provide a valid from/to window")
	}
	sessions := map[string]*readerState{}
	err := filepath.WalkDir(
		options.Directory,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			identity, err := identify(path, options.Harness)
			if err != nil {
				return err
			}
			if identity.id == "" {
				report.SkippedFiles++
				return nil
			}
			if options.Project != "" &&
				filepath.Clean(identity.cwd) != filepath.Clean(options.Project) {
				return nil
			}
			state := sessions[identity.id]
			if state == nil {
				state = newReaderState(options, identity)
				sessions[identity.id] = state
			}
			return eachRecord(
				path,
				func(record object) { state.consume(record) },
				func() { report.UnattributedRecords++ },
			)
		},
	)
	if err != nil {
		return report, err
	}
	keys := make([]string, 0, len(sessions))
	for key := range sessions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := sessions[key]
		report.UnattributedRecords += state.unattributed
		if !state.active {
			continue
		}
		session := state.finish()
		if session.Parent != "" {
			report.ChildSessions = append(report.ChildSessions, session)
			report.Children.add(session.Metrics)
		} else {
			report.Sessions = append(report.Sessions, session)
			report.Primary.add(session.Metrics)
		}
	}
	report.Primary.finish()
	report.Children.finish()
	return report, nil
}

type object map[string]any

func obj(value any) object   { result, _ := value.(map[string]any); return result }
func str(value any) string   { result, _ := value.(string); return result }
func number(value any) int64 { result, _ := value.(float64); return int64(result) }
func array(value any) []any  { result, _ := value.([]any); return result }

func eachRecord(path string, consume func(object), malformed func()) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var record object
			if err := json.Unmarshal(line, &record); err != nil || record == nil {
				malformed()
			} else {
				consume(record)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
	}
}

type sessionIdentity struct{ id, parent, cwd string }

// Metadata can follow Claude bookkeeping rows. Stop at the first usable identity.
func identify(path, harness string) (sessionIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return sessionIdentity{}, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		var record object
		if json.Unmarshal(line, &record) == nil {
			if harness == "codex" && str(record["type"]) == "session_meta" {
				p := obj(record["payload"])
				return sessionIdentity{str(p["id"]), str(p["parent_thread_id"]), str(p["cwd"])}, nil
			}
			if harness == "claude" && str(record["cwd"]) != "" && str(record["sessionId"]) != "" {
				id, parent := str(record["sessionId"]), ""
				if record["isSidechain"] == true ||
					filepath.Base(filepath.Dir(path)) == "subagents" {
					parent = id
					id += "/" + filepath.Base(path)
				}
				return sessionIdentity{id, parent, str(record["cwd"])}, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return sessionIdentity{}, nil
		}
		if readErr != nil {
			return sessionIdentity{}, readErr
		}
	}
}

func limitations(harness string) []string {
	notes := []string{
		"Only saved records are measured; missing logs and unrecorded work are invisible. Window endpoints are inclusive.",
		"Usage is deduplicated by response/message ID within a session. Cumulative token events are excluded. Reasoning is a subset of output.",
		"Child sessions are separate. A parentless worker is indistinguishable from a supervisor; use the project filter and session IDs.",
		"Wake detection uses an explicit Concierge supervisor-event prompt or wake trigger. Later operator steering remains attributed to that turn.",
		"Tool bytes count UTF-8 text returned in saved results, including wrappers. Batched commands share the full result; categories overlap. Unresolved results add zero bytes.",
		"Skill reads count matching call records, not filesystem reads. Interval edges can be partial. Compactions outside the window are excluded.",
		"Pending replies at turn end are not derivable: saved command text and output do not provide an authoritative pending-reply timeline.",
		"Quota is a weekly-limit percentage, not a token balance. Other sessions can contribute. Null observations mean unavailable.",
		"Skipped records include unknown types and invalid relevant records. Malformed JSON has no trusted timestamp and is counted separately as unattributed_records.",
	}
	if harness == "claude" {
		notes = append(
			notes,
			"Claude input includes uncached input, cache creation and cache reads. Reasoning uses thinking_tokens only when saved; zero can mean unavailable.",
			"Claude require_escalated and decoded nonzero command counts are not applicable (null). tool_errors counts is_error results, including non-command tools. Weekly quota is unavailable unless explicitly recorded.",
		)
	} else {
		notes = append(notes, "Codex require_escalated counts literal decoded argument values. Nonzero commands count decoded exit_code results or native Process exited with code results; absent exit codes are unmeasured.")
	}
	return notes
}
