package app

import (
	"fmt"
	"strings"
)

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

type syncLogWriter struct {
	ui *syncProgressUI
}

func (w syncLogWriter) Write(p []byte) (int, error) {
	return w.ui.WriteLog(p)
}
