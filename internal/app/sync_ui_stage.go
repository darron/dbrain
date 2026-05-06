package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
)

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
