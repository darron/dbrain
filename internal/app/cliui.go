package app

import (
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

	"dbrain/internal/sqlitearchive"
)

type cliProgressUI struct {
	out      io.Writer
	tty      bool
	mu       sync.Mutex
	done     chan struct{}
	stage    string
	message  string
	current  int64
	total    int64
	spin     spinner.Model
	bar      progress.Model
	success  lipgloss.Style
	spinnerS lipgloss.Style
}

func newCLIProgressUI(out io.Writer) *cliProgressUI {
	ui := &cliProgressUI{
		out: out,
		spin: spinner.New(
			spinner.WithSpinner(spinner.MiniDot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("39"))),
		),
		bar: progress.New(
			progress.WithDefaultGradient(),
			progress.WithWidth(28),
		),
		success:  lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		spinnerS: lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	}
	if file, ok := out.(*os.File); ok {
		ui.tty = isatty.IsTerminal(file.Fd())
	}
	return ui
}

func (ui *cliProgressUI) Handle(event sqlitearchive.Event) {
	switch event.Kind {
	case sqlitearchive.EventStageStart:
		ui.start(event)
	case sqlitearchive.EventTransferProgress:
		ui.update(event)
	case sqlitearchive.EventStageDone:
		ui.finish(event)
	}
}

func (ui *cliProgressUI) start(event sqlitearchive.Event) {
	ui.stopActive(false, "")
	ui.mu.Lock()
	ui.stage = event.Stage
	ui.message = event.Message
	ui.current = event.Current
	ui.total = event.Total
	if ui.tty {
		ui.done = make(chan struct{})
		done := ui.done
		ui.mu.Unlock()
		go ui.animate(done)
		return
	}
	ui.mu.Unlock()
	_, _ = fmt.Fprintf(ui.out, "• %s\n", event.Message)
}

func (ui *cliProgressUI) update(event sqlitearchive.Event) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if event.Stage != "" {
		ui.stage = event.Stage
	}
	if event.Message != "" {
		ui.message = event.Message
	}
	ui.current = event.Current
	ui.total = event.Total
	if !ui.tty && event.Total > 0 && event.Current >= event.Total {
		_, _ = fmt.Fprintf(ui.out, "  %s\n", ui.transferViewLocked())
	}
}

func (ui *cliProgressUI) finish(event sqlitearchive.Event) {
	ui.stopActive(true, event.Message)
}

func (ui *cliProgressUI) stopActive(success bool, message string) {
	ui.mu.Lock()
	done := ui.done
	ui.done = nil
	ui.mu.Unlock()
	if done != nil {
		close(done)
		time.Sleep(15 * time.Millisecond)
	}
	if done != nil && ui.tty {
		_, _ = fmt.Fprint(ui.out, "\r\033[K")
	}
	if success {
		if strings.TrimSpace(message) == "" {
			ui.mu.Lock()
			message = ui.message
			ui.mu.Unlock()
		}
		_, _ = fmt.Fprintf(ui.out, "%s %s\n", ui.success.Render("✓"), message)
	}
}

func (ui *cliProgressUI) animate(done <-chan struct{}) {
	ticker := time.NewTicker(spinner.MiniDot.FPS)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ui.mu.Lock()
			ui.spin, _ = ui.spin.Update(ui.spin.Tick())
			line := ui.renderLineLocked()
			ui.mu.Unlock()
			_, _ = fmt.Fprintf(ui.out, "\r\033[K%s", line)
		}
	}
}

func (ui *cliProgressUI) renderLineLocked() string {
	if ui.total > 0 {
		return fmt.Sprintf("%s %s  %s", ui.spinnerS.Render(ui.spin.View()), ui.message, ui.transferViewLocked())
	}
	return fmt.Sprintf("%s %s", ui.spinnerS.Render(ui.spin.View()), ui.message)
}

func (ui *cliProgressUI) transferViewLocked() string {
	if ui.total <= 0 {
		return ""
	}
	percent := float64(ui.current) / float64(ui.total)
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}
	return fmt.Sprintf("%s %s/%s", ui.bar.ViewAs(percent), formatBytes(ui.current), formatBytes(ui.total))
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
