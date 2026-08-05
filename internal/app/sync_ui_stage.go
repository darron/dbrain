package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/darron/dbrain/internal/semanticrefresh"
)

func (ui *syncProgressUI) startStageLocked(message string) {
	ui.stopActiveLocked(false, "")
	ui.semantic = false
	ui.message = message
	ui.current = 0
	ui.total = 0
	ui.totalKnown = false
	ui.started = ui.now()
	ui.semanticBaselineCurrent = 0
	ui.semanticBaselineAt = time.Time{}
	ui.semanticETA = 0
	ui.semanticETAKnown = false
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
	} else if wasActive && strings.TrimSpace(message) != "" {
		if ui.tty {
			_, _ = fmt.Fprintf(ui.out, "%s %s\n", ui.err.Render("✗"), message)
		} else {
			_, _ = fmt.Fprintf(ui.out, "%s\n", message)
		}
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
	elapsed := ui.now().Sub(ui.started)
	if elapsed < 0 {
		elapsed = 0
	}
	elapsed = elapsed.Truncate(time.Second)
	if ui.semantic {
		if !ui.totalKnown {
			return fmt.Sprintf("%s %s %s %s", ui.spin.View(), ui.message, ui.muted.Render("planning"), ui.muted.Render("("+elapsed.String()+")"))
		}
		percent := 1.0
		if ui.total > 0 {
			percent = float64(ui.current) / float64(ui.total)
		}
		if percent < 0 {
			percent = 0
		}
		if percent > 1 {
			percent = 1
		}
		line := fmt.Sprintf("%s %s %s %s/%s %s", ui.spin.View(), ui.message, ui.bar.ViewAs(percent), formatCount(ui.current), formatCount(ui.total), ui.muted.Render("("+elapsed.String()+")"))
		if ui.semanticETAKnown {
			line += " " + ui.muted.Render("ETA "+ui.semanticETA.Round(time.Second).String())
		}
		return line
	}
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

func (ui *syncProgressUI) startSemanticProgress(snapshot semanticProgressSnapshot) error {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	ui.startStageLocked(syncStageLabel("semantic " + semanticProgressStageName(snapshot.stage)))
	ui.semantic = true
	ui.semanticBaselineCurrent = max(snapshot.work.Current, 0)
	ui.semanticBaselineAt = ui.started
	ui.applySemanticProgressLocked(snapshot.work)
	if ui.tty {
		ui.redrawActiveLineLocked()
	}
	return nil
}

func (ui *syncProgressUI) updateSemanticProgress(snapshot semanticProgressSnapshot) error {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if !ui.tty {
		ui.writeSideLineLocked(fmt.Sprintf(
			"Semantic %s progress: %s",
			semanticProgressStageName(snapshot.stage),
			semanticProgressFields(snapshot),
		))
		return nil
	}
	ui.applySemanticProgressLocked(snapshot.work)
	ui.redrawActiveLineLocked()
	return nil
}

func (ui *syncProgressUI) finishSemanticProgress(snapshot semanticProgressSnapshot, success bool) error {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	if !ui.tty {
		status := "failed"
		if success {
			status = "complete"
		}
		ui.stopActiveLocked(success, fmt.Sprintf(
			"Semantic %s %s: %s",
			semanticProgressStageName(snapshot.stage),
			status,
			semanticProgressFields(snapshot),
		))
		return nil
	}
	ui.applySemanticProgressLocked(snapshot.work)
	message := strings.TrimPrefix(ui.renderActiveLocked(), ui.spin.View()+" ")
	ui.stopActiveLocked(success, message)
	return nil
}

func (ui *syncProgressUI) applySemanticProgressLocked(work semanticrefresh.StageWork) {
	ui.current = max(work.Current, 0)
	ui.total = max(work.Total, 0)
	ui.totalKnown = work.TotalKnown && ui.total >= ui.current
	ui.semanticETAKnown = false
	if !ui.totalKnown || ui.current < ui.semanticBaselineCurrent {
		return
	}
	elapsed := ui.now().Sub(ui.semanticBaselineAt)
	remaining := ui.total - ui.current
	ui.semanticETA, ui.semanticETAKnown = boundedProgressETA(
		elapsed,
		ui.current-ui.semanticBaselineCurrent,
		remaining,
	)
}
