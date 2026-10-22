package conciergecli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

type reportPreview struct {
	Outcome   string
	Sections  []string
	Decisions []string
}

type reportSection struct {
	level int
	title string
	lines []string
}

// These previews are navigation aids. The saved body remains the evidence.
func previewReport(body string) reportPreview {
	var preview reportPreview
	handoffLevel, categoryLevel, decisionLevel := 0, 0, 0
	for _, section := range reportSections(body) {
		if section.level <= handoffLevel {
			handoffLevel = 0
		}
		if section.level <= categoryLevel {
			categoryLevel = 0
		}
		if section.level <= decisionLevel {
			decisionLevel = 0
		}
		title := strings.ToLower(section.title)
		switch title {
		case "handoff":
			handoffLevel = section.level
		case "outcome":
			if handoffLevel > 0 || (section.level == 2 && preview.Outcome == "") {
				preview.Outcome = previewText(firstParagraph(section.lines), 600)
			}
		case "gaps", "learnings", "artifacts":
			categoryLevel = section.level
		case "open decisions",
			"open questions",
			"waiting on you",
			"operator decisions",
			"decision required":
			decisionLevel = section.level
		}
		if categoryLevel > 0 {
			preview.Sections = append(preview.Sections, previewText(section.title, 120))
		}
		if decisionLevel == 0 {
			continue
		}
		paragraphStart := true
		seenDecision := false
		for _, line := range section.lines {
			line = strings.TrimSpace(line)
			if line == "" {
				paragraphStart = true
				continue
			}
			if (paragraphStart && (section.level == decisionLevel || !seenDecision)) ||
				listItem(line) {
				if section.level > decisionLevel {
					line = section.title + ": " + line
				}
				preview.Decisions = append(preview.Decisions, previewText(line, 240))
				seenDecision = true
			}
			paragraphStart = false
		}
	}
	return preview
}

func reportSections(body string) []reportSection {
	var sections []reportSection
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, trimmed[:1]))]
			continue
		}
		level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if level > 0 && level <= 6 && len(trimmed) > level && trimmed[level] == ' ' {
			title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trimmed[level:]), "#"))
			sections = append(sections, reportSection{level: level, title: title})
			continue
		}
		if len(sections) > 0 {
			last := &sections[len(sections)-1]
			last.lines = append(last.lines, line)
		}
	}
	return sections
}

func firstParagraph(lines []string) string {
	var paragraph []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, line)
	}
	return strings.Join(paragraph, " ")
}

func listItem(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") ||
		strings.HasPrefix(line, "+ ") {
		return true
	}
	digits := strings.TrimLeft(line, "0123456789")
	return len(digits) < len(line) &&
		(strings.HasPrefix(digits, ". ") || strings.HasPrefix(digits, ") "))
}

func previewText(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

func writeReportPreview(output io.Writer, replyID, body, fullCommand string) error {
	preview := previewReport(body)
	if _, err := fmt.Fprintf(output, "reply_id: %s\nbody_bytes: %d\noutcome: %s\n", quote(replyID), len(body), quote(preview.Outcome)); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"sections", preview.Sections}, {"open_decisions", preview.Decisions},
	} {
		if _, err := fmt.Fprintf(output, "%s[%d]:\n", field.name, len(field.values)); err != nil {
			return err
		}
		for _, value := range field.values {
			if _, err := fmt.Fprintf(output, "  - %s\n", quote(value)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(
		output,
		"help: %s\n",
		quote("Preview only; read the full body with "+fullCommand),
	)
	return err
}

func reportShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
