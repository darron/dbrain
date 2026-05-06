package mediadownload

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type downloadProgressWriter struct {
	writer        io.Writer
	logger        *slog.Logger
	ref           model.ItemMediaRef
	contentLength int64
	interval      time.Duration
	byteStep      int64
	startedAt     time.Time
	lastLoggedAt  time.Time
	lastLogged    int64
	written       int64
}

func newDownloadProgressWriter(writer io.Writer, opts progressOptions, ref model.ItemMediaRef, contentLength int64) *downloadProgressWriter {
	if opts.Logger == nil {
		return nil
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultProgressInterval
	}
	byteStep := opts.Bytes
	if byteStep <= 0 {
		byteStep = DefaultProgressBytes
	}
	if contentLength >= 0 && contentLength < byteStep {
		return nil
	}

	startedAt := time.Now().UTC()
	tracker := &downloadProgressWriter{
		writer:        writer,
		logger:        opts.Logger,
		ref:           ref,
		contentLength: contentLength,
		interval:      interval,
		byteStep:      byteStep,
		startedAt:     startedAt,
		lastLoggedAt:  startedAt,
	}
	tracker.log("x media download started")
	return tracker
}

func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	w.maybeLog(false)
	return n, err
}

func (w *downloadProgressWriter) finish() {
	w.maybeLog(true)
}

func (w *downloadProgressWriter) maybeLog(final bool) {
	if w.logger == nil {
		return
	}
	now := time.Now().UTC()
	if !final {
		if w.written-w.lastLogged < w.byteStep && now.Sub(w.lastLoggedAt) < w.interval {
			return
		}
	}
	if !final && w.written == w.lastLogged {
		return
	}
	w.log("x media download progress")
	w.lastLogged = w.written
	w.lastLoggedAt = now
}

func (w *downloadProgressWriter) log(message string) {
	args := []any{
		"item_id", w.ref.ItemID,
		"media_asset_id", w.ref.MediaAssetID,
		"media_type", w.ref.MediaType,
		"bytes", w.written,
		"mb", mbString(w.written),
		"remote_url", w.ref.RemoteURL,
	}
	if w.contentLength > 0 {
		args = append(args,
			"total_bytes", w.contentLength,
			"total_mb", mbString(w.contentLength),
			"percent", percentString(w.written, w.contentLength),
		)
	}
	if !w.startedAt.IsZero() {
		args = append(args, "elapsed", time.Since(w.startedAt).Round(time.Second).String())
	}
	w.logger.Info(message, args...)
}

func mbString(bytes int64) string {
	return formatFloat(float64(bytes) / (1024 * 1024))
}

func percentString(part, total int64) string {
	if total <= 0 {
		return ""
	}
	return formatFloat(float64(part) * 100 / float64(total))
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%.2f", value)
}
