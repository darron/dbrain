package safaritabs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func appleAbsoluteTime(seconds float64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return epoch.Add(time.Duration(seconds * float64(time.Second))).UTC()
}

func appendSampleTitle(titles []string, title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return titles
	}
	if len(titles) >= 10 {
		return titles
	}
	return append(titles, title)
}

func emitProgress(opts Options, event ProgressEvent) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(event)
}

func yearFor(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%04d", t.UTC().Year())
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
