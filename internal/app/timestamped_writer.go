package app

import (
	"bytes"
	"io"
	"sync"
	"time"
)

type timestampedLineWriter struct {
	dst       io.Writer
	now       func() time.Time
	mu        sync.Mutex
	lineStart bool
}

func newTimestampedLineWriter(dst io.Writer, now func() time.Time) io.Writer {
	if dst == nil {
		dst = io.Discard
	}
	if now == nil {
		now = time.Now
	}
	return &timestampedLineWriter{
		dst:       dst,
		now:       now,
		lineStart: true,
	}
}

func (w *timestampedLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var out bytes.Buffer
	for _, b := range p {
		if w.lineStart && b != '\n' {
			out.WriteString(w.now().Format(time.RFC3339))
			out.WriteByte(' ')
			w.lineStart = false
		}
		out.WriteByte(b)
		if b == '\n' {
			w.lineStart = true
		}
	}
	if _, err := w.dst.Write(out.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}
