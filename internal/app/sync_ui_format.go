package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func syncStageLabel(stage string) string {
	switch stage {
	case "import x-bookmarks":
		return "Importing X bookmarks"
	case "hydrate x":
		return "Hydrating X posts and media"
	case "extract links":
		return "Extracting links and enriching sources"
	case "transcribe x-media":
		return "Transcribing social media"
	case "ocr x-photos":
		return "Running OCR on social media"
	case "import github stars":
		return "Importing GitHub stars"
	case "import youtube":
		return "Importing YouTube feeds"
	case "import apple-notes":
		return "Importing Apple Notes"
	case "import safari-tabs":
		return "Importing Safari tabs"
	case "worker sources":
		return "Draining source backlog"
	case "archive media":
		return "Archiving finalized media"
	case "categorize items", "categorize items and sources":
		return "Categorizing items and sources"
	case "semantic projection":
		return "Semantic projection"
	case "semantic embedding":
		return "Semantic embedding"
	case "semantic flush":
		return "Semantic flush"
	case "semantic compaction":
		return "Semantic compaction"
	case "semantic verify":
		return "Semantic verification"
	case "semantic readiness":
		return "Semantic readiness"
	default:
		if strings.HasPrefix(stage, "x settle pass") {
			return "Settling X frontier"
		}
		return stage
	}
}

func (ui *syncProgressUI) applyDebugProgressLocked(line string) {
	if !ui.active || ui.semantic {
		return
	}
	fields := parseKeyValueFields(line)
	processed, ok := parseIntField(fields, "processed")
	if !ok {
		return
	}
	total, ok := parseIntField(fields, "candidates")
	if !ok {
		total, ok = parseIntField(fields, "total")
	}
	if !ok || total <= 0 {
		return
	}
	ui.current = processed
	ui.total = total
}

func parseKeyValueFields(line string) map[string]string {
	fields := map[string]string{}
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		fields[key] = strings.Trim(value, `"`)
	}
	return fields
}

func parseIntField(fields map[string]string, key string) (int64, bool) {
	value, ok := fields[key]
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

func formatCount(value int64) string {
	if value < 1000 {
		return strconv.FormatInt(value, 10)
	}
	return fmt.Sprintf("%.1fk", float64(value)/1000)
}

func (ui *syncProgressUI) formatLogLineLocked(line string) string {
	fields := splitLogFields(line)
	if len(fields) == 0 {
		return ui.muted.Render("  log") + " " + line
	}
	values := map[string]string{}
	var attrs []string
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			attrs = append(attrs, field)
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "time", "level", "msg":
			values[key] = value
		default:
			attrs = append(attrs, key+"="+value)
		}
	}
	level := strings.ToUpper(values["level"])
	if level == "" {
		level = "LOG"
	}
	msg := values["msg"]
	if msg == "" {
		msg = line
	}
	prefix := ui.levelStyle(level).Render(fmt.Sprintf("%-5s", level))
	timestamp := ""
	if values["time"] != "" {
		timestamp = " " + ui.muted.Render(trimLogTimestamp(values["time"]))
	}
	out := "  " + prefix + timestamp + " " + ui.header.Render(msg)
	if len(attrs) > 0 {
		out += " " + ui.attr.Render(strings.Join(attrs, " "))
	}
	return out
}

func (ui *syncProgressUI) levelStyle(level string) lipgloss.Style {
	switch level {
	case "DEBUG":
		return ui.debug
	case "INFO":
		return ui.info
	case "WARN", "WARNING":
		return ui.warn
	case "ERROR":
		return ui.err
	default:
		return ui.muted
	}
}

func trimLogTimestamp(value string) string {
	if len(value) >= len("15:04:05.000") {
		if idx := strings.IndexByte(value, 'T'); idx >= 0 && idx+13 <= len(value) {
			return value[idx+1 : idx+13]
		}
	}
	return value
}

func splitLogFields(line string) []string {
	var fields []string
	var b strings.Builder
	inQuote := false
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			b.WriteRune(r)
			escaped = true
		case r == '"':
			b.WriteRune(r)
			inQuote = !inQuote
		case r == ' ' || r == '\t':
			if inQuote {
				b.WriteRune(r)
				continue
			}
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		fields = append(fields, b.String())
	}
	return fields
}
