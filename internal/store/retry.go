package store

import (
	"context"
	"strings"
	"time"
)

const (
	busyRetryAttempts = 8
	busyRetryDelay    = 100 * time.Millisecond
	busyRetryMaxDelay = 2 * time.Second
)

func withBusyRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	delay := busyRetryDelay

	for attempt := 0; ; attempt++ {
		value, err := fn()
		if err == nil {
			return value, nil
		}
		if !isBusyError(err) || attempt >= busyRetryAttempts-1 {
			return zero, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}

		delay *= 2
		if delay > busyRetryMaxDelay {
			delay = busyRetryMaxDelay
		}
	}
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "sqlite_locked") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}
