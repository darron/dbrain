package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
