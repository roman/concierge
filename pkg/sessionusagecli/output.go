package sessionusagecli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/roman/concierge/pkg/sessionusage"
)

func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).
		Replace(value) +
		`"`
}

type outputOptions struct{ full bool }

func writeTOON(output io.Writer, report sessionusage.Report, options outputOptions) error {
	var buffer bytes.Buffer
	fmt.Fprintf(
		&buffer,
		"harness: %s\nproject: %s\nfrom: %s\nto: %s\n",
		quote(report.Harness),
		quote(report.Project),
		quote(report.From.Format("2006-01-02T15:04:05.999999999Z07:00")),
		quote(report.To.Format("2006-01-02T15:04:05.999999999Z07:00")),
	)
	if options.full {
		for _, item := range []struct {
			name  string
			value sessionusage.Metrics
		}{{"primary", report.Primary}, {"children", report.Children}} {
			fmt.Fprintf(&buffer, "%s:\n", item.name)
			writeObject(&buffer, item.value, "  ")
		}
	} else {
		writeSummary(&buffer, report)
	}
	for _, group := range []struct {
		name     string
		sessions []sessionusage.Session
	}{{"sessions", report.Sessions}, {"child_sessions", report.ChildSessions}} {
		fmt.Fprintf(
			&buffer,
			"%s[%d]{id,requests,input,cached_input,uncached_input,output,reasoning,turns,wake_turns,compactions,skill_reads}:\n",
			group.name,
			len(group.sessions),
		)
		for _, session := range group.sessions {
			u := session.Usage
			fmt.Fprintf(
				&buffer,
				"  %s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
				quote(session.ID),
				u.Requests,
				u.Input,
				u.CachedInput,
				u.UncachedInput,
				u.Output,
				u.Reasoning,
				session.Turns,
				session.WakeTurns,
				session.Compactions,
				session.SkillReads,
			)
		}
		if options.full {
			fmt.Fprintf(&buffer, "%s_details:\n", group.name)
			for _, session := range group.sessions {
				fmt.Fprintf(&buffer, "  %s:\n", quote(session.ID))
				writeObject(&buffer, session, "    ")
			}
		}
	}
	fmt.Fprintf(
		&buffer,
		"skipped_files: %d\nunattributed_records: %d\npending_reply_turn_ends: %s\n",
		report.SkippedFiles,
		report.UnattributedRecords,
		quote(report.PendingReplyTurnEnds),
	)
	if options.full {
		writeArray(&buffer, "limitations", report.Limitations, "")
	} else {
		fmt.Fprintln(&buffer, "help: \"Use --full for session details and metric limits; see docs/session-usage.md.\"")
	}
	_, err := output.Write(buffer.Bytes())
	return err
}

func writeSummary(output io.Writer, report sessionusage.Report) {
	groups := []struct {
		name    string
		metrics sessionusage.Metrics
	}{{"primary", report.Primary}, {"children", report.Children}}
	fmt.Fprintln(
		output,
		"totals[2]{scope,requests,input,cached_input,uncached_input,output,reasoning}:",
	)
	for _, group := range groups {
		u := group.metrics.Usage
		fmt.Fprintf(
			output,
			"  %s,%d,%d,%d,%d,%d,%d\n",
			group.name,
			u.Requests,
			u.Input,
			u.CachedInput,
			u.UncachedInput,
			u.Output,
			u.Reasoning,
		)
	}
	fmt.Fprintln(
		output,
		"activity[2]{scope,turns,wake_turns,skill_reads,compactions,require_escalated,nonzero_commands,tool_errors,skipped_records,duplicate_usage}:",
	)
	for _, group := range groups {
		m := group.metrics
		fmt.Fprintf(
			output,
			"  %s,%d,%d,%d,%d,%s,%s,%s,%d,%d\n",
			group.name,
			m.Turns,
			m.WakeTurns,
			m.SkillReads,
			m.Compactions,
			nullableCount(m.RequireEscalated),
			nullableCount(m.NonzeroCommands),
			nullableCount(m.ToolErrors),
			m.SkippedRecords,
			m.DuplicateUsage,
		)
	}
	fmt.Fprintln(
		output,
		"wakes[2]{scope,requests,input,cached_input,uncached_input,output,reasoning}:",
	)
	for _, group := range groups {
		u := group.metrics.WakeUsage
		fmt.Fprintf(
			output,
			"  %s,%d,%d,%d,%d,%d,%d\n",
			group.name,
			u.Requests,
			u.Input,
			u.CachedInput,
			u.UncachedInput,
			u.Output,
			u.Reasoning,
		)
	}
	fmt.Fprintln(output, "per_wake_turn[2]{scope,requests,input,output,tokens}:")
	for _, group := range groups {
		m := group.metrics.PerWakeTurn
		fmt.Fprintf(
			output,
			"  %s,%g,%g,%g,%g\n",
			group.name,
			m.Requests,
			m.Input,
			m.Output,
			m.Tokens,
		)
	}
	fmt.Fprintln(output, "tool_output[6]{scope,operation,calls,results,bytes,bytes_per_call}:")
	for _, group := range groups {
		for _, category := range []string{"claim", "reply_check", "report"} {
			m := group.metrics.ToolOutput[category]
			fmt.Fprintf(
				output,
				"  %s,%s,%d,%d,%d,%g\n",
				group.name,
				category,
				m.Calls,
				m.Results,
				m.Bytes,
				m.BytesPerCall,
			)
		}
	}
	fmt.Fprintln(
		output,
		"weekly_quota[2]{scope,first_time,first_used_percent,last_time,last_used_percent}:",
	)
	for _, group := range groups {
		q := group.metrics.WeeklyQuota
		fmt.Fprintf(output, "  %s,%s,%s\n", group.name, quotaCells(q.First), quotaCells(q.Last))
	}
}

func nullableCount(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(*value)
}

func quotaCells(value *sessionusage.Observation) string {
	if value == nil {
		return "null,null"
	}
	return fmt.Sprintf(
		"%s,%g",
		quote(value.Time.Format("2006-01-02T15:04:05.999999999Z07:00")),
		value.UsedPercent,
	)
}

// This report schema contains nested objects and primitive arrays only.
func writeObject(output io.Writer, value any, indent string) {
	data, _ := json.Marshal(value)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := fields[key]
		if len(field) > 0 && field[0] == '{' {
			fmt.Fprintf(output, "%s%s:\n", indent, key)
			writeObject(output, field, indent+"  ")
		} else if len(field) > 0 && field[0] == '[' {
			var elements []any
			_ = json.Unmarshal(field, &elements)
			writeArray(output, key, elements, indent)
		} else {
			fmt.Fprintf(output, "%s%s: %s\n", indent, key, scalar(field))
		}
	}
}

func writeArray(output io.Writer, key string, value any, indent string) {
	data, _ := json.Marshal(value)
	var elements []json.RawMessage
	_ = json.Unmarshal(data, &elements)
	parts := make([]string, len(elements))
	for index, element := range elements {
		parts[index] = scalar(element)
	}
	fmt.Fprintf(output, "%s%s[%d]: %s\n", indent, key, len(elements), strings.Join(parts, ","))
}

func scalar(value json.RawMessage) string {
	if len(value) > 0 && value[0] == '"' {
		var text string
		_ = json.Unmarshal(value, &text)
		return quote(text)
	}
	return string(value)
}
