package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

type syncProgressUI struct {
	out     io.Writer
	tty     bool
	mu      sync.Mutex
	done    chan struct{}
	active  bool
	message string
	current int64
	total   int64
	started time.Time
	spin    spinner.Model
	bar     progress.Model
	success lipgloss.Style
	accent  lipgloss.Style
	muted   lipgloss.Style
	header  lipgloss.Style
	debug   lipgloss.Style
	info    lipgloss.Style
	warn    lipgloss.Style
	err     lipgloss.Style
	attr    lipgloss.Style
	lineBuf bytes.Buffer
	logBuf  bytes.Buffer
}

func newSyncProgressUI(out io.Writer) *syncProgressUI {
	ui := &syncProgressUI{
		out: out,
		spin: spinner.New(
			spinner.WithSpinner(spinner.MiniDot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("39"))),
		),
		bar: progress.New(
			progress.WithDefaultGradient(),
			progress.WithWidth(24),
		),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		accent:  lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		header:  lipgloss.NewStyle().Bold(true),
		debug:   lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		info:    lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		err:     lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		attr:    lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	}
	if file, ok := out.(*os.File); ok {
		ui.tty = isatty.IsTerminal(file.Fd())
	}
	return ui
}

func (ui *syncProgressUI) Write(p []byte) (int, error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			line := ui.lineBuf.String()
			ui.lineBuf.Reset()
			ui.handleProgressLineLocked(strings.TrimRight(line, "\r"))
			continue
		}
		_ = ui.lineBuf.WriteByte(b)
	}
	return len(p), nil
}

func (ui *syncProgressUI) LogWriter() io.Writer {
	return syncLogWriter{ui: ui}
}

func (ui *syncProgressUI) Close() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ui.lineBuf.Len() > 0 {
		line := ui.lineBuf.String()
		ui.lineBuf.Reset()
		ui.handleProgressLineLocked(line)
	}
	if ui.logBuf.Len() > 0 {
		line := ui.logBuf.String()
		ui.logBuf.Reset()
		ui.writeLogLineLocked(line)
	}
	ui.stopActiveLocked(false, "")
}

func (ui *syncProgressUI) WriteLog(p []byte) (int, error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			line := ui.logBuf.String()
			ui.logBuf.Reset()
			ui.writeLogLineLocked(strings.TrimRight(line, "\r"))
			continue
		}
		_ = ui.logBuf.WriteByte(b)
	}
	return len(p), nil
}

func (ui *syncProgressUI) handleProgressLineLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "Sync started at ") {
		ui.stopActiveLocked(false, "")
		_, _ = fmt.Fprintf(ui.out, "%s %s\n", ui.accent.Render("◆"), ui.header.Render(line))
		return
	}
	if strings.HasPrefix(line, "==> ") {
		ui.startStageLocked(syncStageLabel(strings.TrimSpace(strings.TrimPrefix(line, "==> "))))
		return
	}
	if strings.Contains(line, " complete:") || strings.HasPrefix(line, "Sync completed in ") {
		ui.stopActiveLocked(true, line)
		return
	}
	ui.writeSideLineLocked(line)
}

func (ui *syncProgressUI) startStageLocked(message string) {
	ui.stopActiveLocked(false, "")
	ui.message = message
	ui.current = 0
	ui.total = 0
	ui.started = time.Now()
	ui.active = true
	if ui.tty {
		ui.done = make(chan struct{})
		done := ui.done
		go ui.animate(done)
		return
	}
	_, _ = fmt.Fprintf(ui.out, "• %s\n", message)
}

func (ui *syncProgressUI) stopActiveLocked(success bool, message string) {
	done := ui.done
	ui.done = nil
	wasActive := ui.active
	ui.active = false
	if done != nil {
		close(done)
	}
	if done != nil && ui.tty {
		_, _ = fmt.Fprint(ui.out, "\r\033[K")
	}
	if success {
		if strings.TrimSpace(message) == "" {
			message = ui.message
		}
		_, _ = fmt.Fprintf(ui.out, "%s %s\n", ui.success.Render("✓"), message)
	} else if wasActive && !ui.tty && strings.TrimSpace(message) != "" {
		_, _ = fmt.Fprintf(ui.out, "%s\n", message)
	}
}

func (ui *syncProgressUI) animate(done <-chan struct{}) {
	ticker := time.NewTicker(spinner.MiniDot.FPS)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ui.mu.Lock()
			if !ui.active {
				ui.mu.Unlock()
				continue
			}
			ui.spin, _ = ui.spin.Update(ui.spin.Tick())
			line := ui.renderActiveLocked()
			_, _ = fmt.Fprintf(ui.out, "\r\033[K%s", line)
			ui.mu.Unlock()
		}
	}
}

func (ui *syncProgressUI) renderActiveLocked() string {
	elapsed := time.Since(ui.started).Truncate(time.Second)
	if ui.total > 0 {
		percent := float64(ui.current) / float64(ui.total)
		if percent < 0 {
			percent = 0
		}
		if percent > 1 {
			percent = 1
		}
		return fmt.Sprintf("%s %s %s %s/%s %s", ui.spin.View(), ui.message, ui.bar.ViewAs(percent), formatCount(ui.current), formatCount(ui.total), ui.muted.Render("("+elapsed.String()+")"))
	}
	return fmt.Sprintf("%s %s %s", ui.spin.View(), ui.message, ui.muted.Render("("+elapsed.String()+")"))
}

func (ui *syncProgressUI) writeLogLineLocked(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	ui.applyDebugProgressLocked(line)
	redraw := ui.clearActiveLineLocked()
	_, _ = fmt.Fprintln(ui.out, ui.formatLogLineLocked(line))
	if redraw {
		ui.redrawActiveLineLocked()
	}
}

func (ui *syncProgressUI) writeSideLineLocked(line string) {
	redraw := ui.clearActiveLineLocked()
	_, _ = fmt.Fprintf(ui.out, "%s %s\n", ui.muted.Render("•"), line)
	if redraw {
		ui.redrawActiveLineLocked()
	}
}

func (ui *syncProgressUI) clearActiveLineLocked() bool {
	if !ui.tty || !ui.active {
		return false
	}
	_, _ = fmt.Fprint(ui.out, "\r\033[K")
	return true
}

func (ui *syncProgressUI) redrawActiveLineLocked() {
	if !ui.tty || !ui.active {
		return
	}
	_, _ = fmt.Fprintf(ui.out, "\r\033[K%s", ui.renderActiveLocked())
}

func syncStageLabel(stage string) string {
	switch stage {
	case "import x-bookmarks":
		return "Importing X bookmarks"
	case "hydrate x":
		return "Hydrating X posts and media"
	case "extract links":
		return "Extracting links and enriching sources"
	case "transcribe x-media":
		return "Transcribing X media"
	case "ocr x-photos":
		return "Running OCR on X photos"
	case "import github stars":
		return "Importing GitHub stars"
	case "import youtube":
		return "Importing YouTube feeds"
	case "import apple-notes":
		return "Importing Apple Notes"
	case "worker sources":
		return "Draining source backlog"
	case "archive media":
		return "Archiving finalized media"
	case "categorize items", "categorize items and sources":
		return "Categorizing items and sources"
	default:
		if strings.HasPrefix(stage, "x settle pass") {
			return "Settling X frontier"
		}
		return stage
	}
}

func (ui *syncProgressUI) applyDebugProgressLocked(line string) {
	if !ui.active {
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

type syncLogWriter struct {
	ui *syncProgressUI
}

func (w syncLogWriter) Write(p []byte) (int, error) {
	return w.ui.WriteLog(p)
}
